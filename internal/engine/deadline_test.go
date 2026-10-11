package engine_test

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestADeadlineExpiredRunLeavesNoProviderProcessBehind holds the D11 road-test
// finding: stopping Terraform at its per-test deadline used to kill the
// Terraform process alone, so every provider plugin it had started was
// orphaned — reparented, still running, and still executing the plugin cache's
// binary, which made the next `terraform init` that installed that provider
// version fail with "text file busy".
//
// The suite runs against the real Terraform and the real null provider: the
// provider plugin is a real grandchild process, so the assertion is about
// processes, not output. The invocation's plugin cache directory is private to
// this test — the harness gives every test its own — and a provider process
// executes the cached binary from it, so a process whose executable lives
// under that directory after Run returns can only be this run's leak. A
// process caught mid-death gets a short grace: the scan re-checks until the
// sightings persist.
func TestADeadlineExpiredRunLeavesNoProviderProcessBehind(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	if runtime.GOOS != "linux" {
		t.Skip("the leak scan reads /proc/<pid>/exe, which is Linux-only; the group kill itself is exercised by every provider-backed test")
	}

	module := copyFixture(t, "unmocked-timeout")

	config := baseConfig(t, module)
	// A budget the suite cannot meet, with the factor collapsed so the floor
	// is the whole budget: Terraform is killed mid-suite, with the provider
	// plugin long since started (it starts within the first second; the suite
	// runs for seconds after it).
	config.TimeoutFactor = 0.0001
	config.TimeoutFloor = 2 * time.Second
	// Two mutants, one per trigger: the region mutant's runs all pass, so its
	// deadline lands with no failed run and the invocation is classified by
	// the deadline alone — the timeout state is the run's observable trace of
	// the expired budget.
	config.IncludeOperators = []string{string(mutation.StrCase)}
	// The null provider is unmocked by design — the plugin must be a real
	// process for the leak to be observable — so the run needs the same
	// opt-in every real-provider test here uses.
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Count(report.Timeout) == 0 {
		t.Fatalf("expected at least one timeout, got %v", result.Metrics.Counts)
	}

	cacheDir := pluginCacheDir(t, config.Env)

	deadline := time.Now().Add(2 * time.Second)
	for {
		leaked := executablesUnder(cacheDir)
		if len(leaked) == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("provider processes outlived the run they were started by: %v", leaked)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// TestAMutantWhoseTestReachedItsDeadlineIsNamedInTheReport holds the second
// half of the D11 road-test finding: the report used to carry no trace of a
// deadline the runs' verdicts had outranked. A mutant killed at its deadline
// was reported as Killed with nothing to show its test had been cut short, so
// a reader could not tell a fully-executed kill from one the budget ended.
//
// The assertion is made against the JSON document, the published surface, and
// not against a struct field: before the fix the field does not exist, and
// the document must be shown to carry the deadline whatever the Go type does.
func TestAMutantWhoseTestReachedItsDeadlineIsNamedInTheReport(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "unmocked-timeout")

	config := baseConfig(t, module)
	config.TimeoutFactor = 0.0001
	config.TimeoutFloor = 3 * time.Second
	// The env mutant's runs fail their assertion from the first completed run
	// onwards, so its deadline lands behind failed runs and the verdict is
	// Killed; the region mutant's runs all pass, so its verdict is Timeout.
	// One invocation produces both precedence shapes at the same deadline.
	config.IncludeOperators = []string{string(mutation.StrCase)}
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Count(report.Killed) == 0 || result.Count(report.Timeout) == 0 {
		t.Fatalf("expected at least one killed and one timeout verdict, got %v", result.Metrics.Counts)
	}

	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	var document struct {
		Mutants []struct {
			ID              string `json:"id"`
			State           string `json:"state"`
			DeadlineReached *bool  `json:"deadline_reached"`
		} `json:"mutants"`
	}
	if err := json.Unmarshal([]byte(encoded.String()), &document); err != nil {
		t.Fatalf("decoding the report document: %v", err)
	}

	for _, mutant := range document.Mutants {
		if mutant.DeadlineReached == nil || !*mutant.DeadlineReached {
			t.Fatalf("mutant %s (state %s) does not name its deadline in the document", mutant.ID, mutant.State)
		}
	}
}

// pluginCacheDir reads the plugin cache directory back out of the request the
// test will run, so the scan watches the directory this run's plugins are
// installed into and no other test's.
func pluginCacheDir(t *testing.T, environment []string) string {
	t.Helper()

	const prefix = "TF_PLUGIN_CACHE_DIR="

	for _, entry := range environment {
		if path, found := strings.CutPrefix(entry, prefix); found {
			return path
		}
	}

	t.Fatalf("the request carries no %s entry: %v", prefix, environment)

	return ""
}

// executablesUnder names every process whose executable lives under dir.
func executablesUnder(dir string) []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	var found []string

	for _, entry := range entries {
		if !isNumeric(entry.Name()) {
			continue
		}

		executable, linkErr := os.Readlink("/proc/" + entry.Name() + "/exe")
		if linkErr != nil {
			continue
		}

		if strings.HasPrefix(executable, dir+string(os.PathSeparator)) {
			found = append(found, executable)
		}
	}

	return found
}

func isNumeric(name string) bool {
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}

	return name != ""
}
