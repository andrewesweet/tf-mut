package engine_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
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
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

	requireProviderMirror(t)

	if runtime.GOOS != "linux" {
		t.Skip("the leak scan reads /proc/<pid>/exe, which is Linux-only; " +
			"the group kill itself is exercised by every provider-backed test")
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

		// Real elapsed time: the kernel reaps the providers asynchronously;
		// nothing signals their exit, so the poll waits.
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
// deadlineDocument is the slice of the published document the JSON assertion
// reads: named types, because the assertion is about the wire shape.
type deadlineDocument struct {
	Mutants []deadlineMutant `json:"mutants"`
}

type deadlineMutant struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	DeadlineReached *bool  `json:"deadline_reached"`
}

func TestAMutantWhoseTestReachedItsDeadlineIsNamedInTheReport(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

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

	// Typed: the field is on every mutant the run executed, whatever its
	// verdict — the whole point of the flag.
	for _, mutant := range result.Mutants {
		if !mutant.DeadlineReached {
			t.Fatalf("mutant %s (state %s) reached no deadline in the typed report", mutant.ID, mutant.State)
		}
	}

	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	var document deadlineDocument
	if err := json.Unmarshal([]byte(encoded.String()), &document); err != nil {
		t.Fatalf("decoding the report document: %v", err)
	}

	for _, mutant := range document.Mutants {
		if mutant.DeadlineReached == nil || !*mutant.DeadlineReached {
			t.Fatalf("mutant %s (state %s) does not name its deadline in the document", mutant.ID, mutant.State)
		}
	}
}

// TestTheTerminalReportNamesTheMutantsWhoseTestReachedItsDeadline renders the
// same invocation as the JSON case through the terminal reporter: the summary
// must name the deadline behind whatever verdicts the finished runs earned.
func TestTheTerminalReportNamesTheMutantsWhoseTestReachedItsDeadline(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

	requireProviderMirror(t)

	module := copyFixture(t, "unmocked-timeout")

	config := baseConfig(t, module)
	config.TimeoutFactor = 0.0001
	config.TimeoutFloor = 3 * time.Second
	config.IncludeOperators = []string{string(mutation.StrCase)}
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	reached := 0
	for _, mutant := range result.Mutants {
		if mutant.DeadlineReached {
			reached++
		}
	}

	terminal := bytes.Buffer{}
	if err := report.WriteTerminal(&terminal, result); err != nil {
		t.Fatalf("writing terminal report: %v", err)
	}

	rendered := terminal.String()
	if !strings.Contains(rendered, fmt.Sprintf("Deadline reached %d", reached)) {
		t.Fatalf("the terminal report does not name the %d deadline(s):\n%s", reached, rendered)
	}

	for _, mutant := range result.Mutants {
		if !mutant.DeadlineReached {
			continue
		}

		line := fmt.Sprintf("%s  %s:%d  %s", mutant.Operator, mutant.Range.File, mutant.Range.Start.Line, mutant.State)
		if !strings.Contains(rendered, line) {
			t.Fatalf("the terminal report omits the deadline row for %s (%s):\n%s", mutant.ID, line, rendered)
		}
	}
}

// TestAReportWithNoDeadlineNamesNone keeps the summary quiet when no test
// reached its budget: the section exists only when there is something to say.
func TestAReportWithNoDeadlineNamesNone(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	terminal := bytes.Buffer{}
	if err := report.WriteTerminal(&terminal, result); err != nil {
		t.Fatalf("writing terminal report: %v", err)
	}

	if strings.Contains(terminal.String(), "Deadline reached") {
		t.Fatalf("the terminal report names a deadline no test reached:\n%s", terminal.String())
	}
}

// TestTheDeadlineReplaysFromTheCache holds the cache half of the visibility
// contract: a stored verdict carries its deadline flag, the replay projects it
// onto the mutant, and an entry from before the flag — which cannot carry one
// — is a miss rather than a silent loss. The stored shape is staged directly:
// a killed verdict whose invocation reached its deadline is exactly the shape
// no cacheable run can produce (real-provider runs are uncacheable under the
// unsafe-opt-in rule, and a cacheable invocation slow enough to reach its
// budget would be a fixture lie), so the entry is crafted on disk the way
// TestPreSandboxFixInvalidVerdictsAreNotReplayed crafts its legacy entries.
func TestTheDeadlineReplaysFromTheCache(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	cachedRun(t, config)

	entries := deadlineEntries(t, module)
	if len(entries) == 0 {
		t.Fatal("the first run wrote no cache entries")
	}

	for _, path := range entries {
		patchEntry(t, path, map[string]any{"deadline_reached": true})
	}

	second := cachedRun(t, config)
	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("the second run did not replay the patched entries: %+v", second.Population)
	}

	for _, mutant := range second.Mutants {
		if mutant.State == report.Ignored || mutant.State == report.NoCoverage {
			continue
		}

		if !mutant.DeadlineReached {
			t.Fatalf("the replayed mutant %s (%s) lost its deadline", mutant.ID, mutant.State)
		}
	}

	// An entry from before the flag cannot carry one; replaying it would
	// manufacture a verdict whose deadline was never measured. The format
	// version moved with the flag, so such an entry is a miss.
	for _, path := range entries {
		patchEntry(t, path, map[string]any{
			"deadline_reached": true,
			"format_version":   "tf-mut-cache-2",
		})
	}

	third := cachedRun(t, config)
	if third.Population.Cached != 0 || third.Population.Fresh == 0 {
		t.Fatalf("an entry that predates the flag was replayed: %+v", third.Population)
	}

	for _, mutant := range third.Mutants {
		if mutant.DeadlineReached {
			t.Fatalf("the freshly executed mutant %s (%s) claims a deadline it never reached",
				mutant.ID, mutant.State)
		}
	}
}

// deadlineEntries names every cache entry file the module's cache holds.
func deadlineEntries(t *testing.T, module string) []string {
	t.Helper()

	dir := cacheDir(module)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	var paths []string

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}

	return paths
}

// patchEntry rewrites named members of one cache entry on disk.
func patchEntry(t *testing.T, path string, patch map[string]any) {
	t.Helper()

	content, readErr := os.ReadFile(filepath.Clean(path))
	if readErr != nil {
		t.Fatalf("reading cache entry: %v", readErr)
	}

	document := map[string]any{}
	if decodeErr := json.Unmarshal(content, &document); decodeErr != nil {
		t.Fatalf("decoding cache entry: %v", decodeErr)
	}

	maps.Copy(document, patch)

	encoded, encodeErr := json.Marshal(document)
	if encodeErr != nil {
		t.Fatalf("encoding cache entry: %v", encodeErr)
	}

	testsupport.WriteFile(t, path, string(encoded))
}

// pluginCacheDir reads the plugin cache directory back out of the request the

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
