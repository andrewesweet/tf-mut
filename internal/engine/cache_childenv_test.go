package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Commit 1 of the child-environment key: the key hashes exactly the
// environment tfexec hands the child — git location variables stripped,
// performance defaults included — so the key and the child can no longer
// disagree. These tests are sequential: they mutate the test process's own
// environment, which parallel tests must never observe mid-change.

// TestAGitLocationVariableDoesNotChangeTheKey pins the sound half of commit
// 1: a variable the child never receives cannot be a miss. GIT_DIR redirects
// Terraform's git subprocesses at the caller's repository and is stripped
// from the inherited environment, so setting it between two otherwise
// identical runs must replay every verdict.
func TestAGitLocationVariableDoesNotChangeTheKey(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}

	t.Setenv("GIT_DIR", t.TempDir())

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("setting GIT_DIR missed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)
}

// TestTheKeyedEnvironmentCarriesThePerformanceDefaults pins the complete
// half of commit 1: the child always runs under GOGC, GOMEMLIMIT and
// TF_LOG_SDK, whether the caller set them or the defaults applied, so the
// keyed environment — and the miss-explanation manifest derived from it —
// carries all three names.
func TestTheKeyedEnvironmentCarriesThePerformanceDefaults(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	manifest := readOnlyManifest(t, module)

	for _, name := range []string{"GOGC", "GOMEMLIMIT", "TF_LOG_SDK"} {
		if _, found := manifest.Env[name]; !found {
			t.Fatalf("the keyed environment omits %s; manifest names are %q",
				name, manifestKeys(manifest))
		}
	}
}

// TestAStrippedVariableStaysOutOfTheMissExplanation: with the key on the
// child environment, a stripped name is absent from both generations'
// digests, so a transition that changes only stripped names explains
// nothing — there is no miss to explain.
func TestAStrippedVariableStaysOutOfTheMissExplanation(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	t.Setenv("GIT_DIR", t.TempDir())

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 {
		t.Fatal("a stripped-only transition missed; the case is vacuous")
	}

	if second.CacheMiss != nil {
		t.Fatalf("a hit carries a miss explanation: %+v", second.CacheMiss)
	}

	manifest := readOnlyManifest(t, module)
	if _, found := manifest.Env["GIT_DIR"]; found {
		t.Fatal("a stripped variable reaches the manifest digests")
	}
}

type envManifest struct {
	Env map[string]string `json:"env"`
}

func readOnlyManifest(t *testing.T, module string) envManifest {
	t.Helper()

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "-manifest.json") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(cacheDir(module), entry.Name()))
		if err != nil {
			t.Fatalf("reading manifest: %v", err)
		}

		var manifest envManifest
		if err := json.Unmarshal(content, &manifest); err != nil {
			t.Fatalf("decoding manifest: %v", err)
		}

		return manifest
	}

	t.Fatal("no generation manifest was stored")

	return envManifest{}
}

func manifestKeys(manifest envManifest) []string {
	names := make([]string, 0, len(manifest.Env))
	for name := range manifest.Env {
		names = append(names, name)
	}

	return names
}

// unsetProcessEnv removes one variable from the test process's environment,
// restoring it when the test ends. The end-to-end tests read the real
// process environment, so a variable the Go toolchain or the developer's
// shell exported would otherwise make a removal unobservable.
//
//nolint:usetesting // t.Setenv would undo this restoration.
func unsetProcessEnv(t *testing.T, name string) {
	t.Helper()

	previous, wasSet := os.LookupEnv(name)
	if wasSet {
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}

	t.Cleanup(func() {
		if !wasSet {
			return
		}

		if err := os.Setenv(name, previous); err != nil {
			t.Errorf("restoring %s: %v", name, err)
		}
	})
}

// strippedSweep is every session bookkeeping name the child no longer
// receives, with a value a verdict would react to if any process in the
// Terraform tree could still see it: tracePropagation McCabe identifiers,
// shell depths, multiplexer sockets, unit invocations, SSH endpoints.
//
//nolint:gosec // OLDPWD trips the credential pattern; the values are session fixtures.
func strippedSweep() map[string]string {
	return map[string]string{
		"TRACEPARENT":           "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0b902b7-01",
		"TRACESTATE":            "rojo=00f067aa0b902b7,congo=t61rcWkgMzE",
		"SHLVL":                 "99",
		"OLDPWD":                "/home/dev/tf-mut/docs with spaces",
		"_":                     "/usr/bin/terraform",
		"TMUX":                  "/tmp/tmux-1000/attack,9999,0",
		"TMUX_PANE":             "%9",
		"TERM_PROGRAM":          "screen",
		"TERM_PROGRAM_VERSION":  "5.0",
		"INVOCATION_ID":         "ffffffffffffffffffffffffffffffff",
		"SYSTEMD_EXEC_PID":      "99999",
		"MEMORY_PRESSURE_WATCH": "/sys/fs/cgroup/other.slice/memory.pressure",
		"MEMORY_PRESSURE_WRITE": "/sys/fs/cgroup/other.slice/cgroup.pressure",
		"JOURNAL_STREAM":        "9:99999",
		"SSH_CLIENT":            "203.0.113.7 60000 22",
		"SSH_CONNECTION":        "203.0.113.7 60000 198.51.100.2 22",
		"SSH_TTY":               "/dev/pts/9",
		"XDG_SESSION_ID":        "c99",
	}
}

// TestEveryStrippedVariableTogetherCannotChangeTheVerdict is the false-reuse
// proof: every session bookkeeping variable changed at once, to values no
// verdict could survive seeing, and the second run still replays every
// verdict byte-identical. A hit whose verdict differed would be a false reuse
// (M4 M5); this test counts them at zero.
//
//nolint:paralleltest // mutates the test process's environment; t.Setenv forbids t.Parallel.
func TestEveryStrippedVariableTogetherCannotChangeTheVerdict(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}

	for name := range strippedSweep() {
		unsetProcessEnv(t, name)
	}

	for name, value := range strippedSweep() {
		t.Setenv(name, value)
	}

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("changing only stripped variables missed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)
}

// TestSystemdToSystemdIdentifiersCannotChangeTheVerdict replays the recorded
// d4-1 to d4-2 transition at the engine seam: only per-invocation systemd
// identifiers differ, so the run replays and the verdicts are identical.
func TestSystemdToSystemdIdentifiersCannotChangeTheVerdict(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)

	t.Setenv("INVOCATION_ID", "22222222222222222222222222222222")
	t.Setenv("SYSTEMD_EXEC_PID", "200")
	t.Setenv("MEMORY_PRESSURE_WATCH", "/sys/fs/cgroup/system.slice/memory.pressure")

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("changing only systemd identifiers missed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)
}

// TestDebugLoggingDoesNotCostTheCache replays U1 to U2: enabling tf-mut's
// own debug logging used to discard the cache it was meant to debug, because
// TF_MUT_DEBUG entered the key through the process environment. The CLI reads
// it (`cmd/tf-mut/main.go`), Terraform never receives it, so it is stripped.
func TestDebugLoggingDoesNotCostTheCache(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)

	t.Setenv("TF_MUT_DEBUG", "1")
	t.Setenv("TRACEPARENT", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("enabling debug logging missed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)
}

// TestATfVarSetInOneSessionAndAbsentInTheNextStillMisses: TF_VAR_* is the
// verdict-relevant in-process read — discovery consults the process
// environment directly — so it is never stripped, and a session that stops
// exporting it misses and names it as removed.
func TestATfVarSetInOneSessionAndAbsentInTheNextStillMisses(t *testing.T) {
	module := copyFixture(t, "operators")
	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(mutation.StrCase)}

	const probeName = "TF_VAR_names"
	unsetProcessEnv(t, probeName)
	t.Setenv(probeName, `["alpha","beta"]`)

	// No verdict comparison: the first run had the override and the second
	// does not, so their inputs genuinely differ.
	cachedRun(t, config)
	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 {
		t.Fatal("the unchanged run replayed nothing; the case is vacuous")
	}

	if err := os.Unsetenv(probeName); err != nil {
		t.Fatalf("unsetting %s: %v", probeName, err)
	}

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a TF_VAR_* removal", second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("a TF_VAR_* removal missed without an explanation")
	}

	if !slices.Contains(second.CacheMiss.Removed, probeName) {
		t.Fatalf("the miss names removed %q, which omits %q",
			second.CacheMiss.Removed, probeName)
	}
}

// TestAMixedTransitionMissesButNamesOnlyForwardedNames: a transition that
// changes stripped and forwarded variables together still misses — any doubt
// is a miss — but the explanation names only the forwarded name. The
// stripped name reaches neither the key nor the manifest, so there is
// nothing to name it from.
func TestAMixedTransitionMissesButNamesOnlyForwardedNames(t *testing.T) {
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	const probeName = "TFMUT_CACHE_PROBE"
	const probeValue = "probe-value-mixed-9c41"
	config.Env = append(config.Env, probeName+"="+probeValue)
	t.Setenv("TRACEPARENT", "00-dddddddddddddddddddddddddddddddd-eeeeeeeeeeeeeeee-01")

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a forwarded environment change",
			second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("an environment-only cache miss named no variable")
	}

	for _, list := range [][]string{
		second.CacheMiss.Added, second.CacheMiss.Removed, second.CacheMiss.Changed,
	} {
		if slices.Contains(list, "TRACEPARENT") {
			t.Fatalf("the miss explanation names a stripped variable: %+v", second.CacheMiss)
		}
	}

	if !slices.Contains(second.CacheMiss.Added, probeName) {
		t.Fatalf("the miss names added %q, which omits %q",
			second.CacheMiss.Added, probeName)
	}
}
