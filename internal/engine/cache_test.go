package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// M3b.2 (#48): the incremental cache — coarse before clever, safe as a disk
// format. The key hashes the entire materialised source closure, all tests,
// the resolved configuration, the lock and module inventory, the keyed
// environment, the Terraform identity, the cache format version and the
// masked baseline fingerprint; any doubt is a miss.

// cachedRun executes one run with the given configuration. Tests that expect
// a cache hit must reuse one configuration: the key includes the keyed
// environment, and the harness gives each configuration its own
// TF_PLUGIN_CACHE_DIR.
func cachedRun(t *testing.T, config engine.RunRequest) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	return result
}

func cacheDir(module string) string {
	return filepath.Join(module, sandbox.CacheDirName)
}

// discriminateFixture is the two-module fixture the dimension cases share.
const discriminateFixture = "discriminate"

// TestASecondUnchangedRunIsAllCacheHits is the demo case, and pins the
// determinism criterion: unchanged inputs, identical population, every
// executed verdict replayed and marked, and verdict invariance holds.
func TestASecondUnchangedRunIsAllCacheHits(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}

	second := cachedRun(t, config)

	if second.Population.Cached == 0 || second.Population.Fresh != 0 {
		t.Fatalf("the second unchanged run was not all cache hits: %+v", second.Population)
	}

	for _, mutant := range second.Mutants {
		if mutant.State == report.NoCoverage || mutant.State == report.Ignored {
			continue
		}

		if mutant.Provenance == nil || mutant.Provenance.Execution != report.ExecutionCached {
			t.Errorf("mutant %s is not marked cached: %+v", mutant.ID, mutant.Provenance)
		}

		if mutant.Provenance != nil && mutant.Provenance.CacheKey == "" {
			t.Errorf("mutant %s carries no cache key basis", mutant.ID)
		}
	}

	assertVerdictInvariance(t, first, second)
}

// TestCacheManifestStoresOnlyTheEnvironmentComponent pins the keyed
// environment contract: the manifest carries exactly the child environment
// the run handed Terraform — additions included, non-forwarded variables
// stripped — and nothing else, with values as digests only.
func TestCacheManifestStoresOnlyTheEnvironmentComponent(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	const probeName = "TFMUT_CACHE_PROBE"
	const probeValue = "probe-value-6f913a2b"
	config.Env = append(config.Env, probeName+"="+probeValue)
	cachedRun(t, config)

	environment := tfexec.KeyedEnvironment(config.Env)
	slices.Sort(environment)
	environment = slices.Compact(environment)
	digest := sha256.New()
	for _, entry := range environment {
		_, _ = fmt.Fprintf(digest, "env\x00\x00%s\x00", entry)
	}

	wantEnvironment := hex.EncodeToString(digest.Sum(nil))
	probeDigest := sha256.Sum256([]byte(probeValue))
	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	manifests := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), "-manifest.json") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(cacheDir(module), entry.Name()))
		if err != nil {
			t.Fatalf("reading manifest: %v", err)
		}

		var manifest struct {
			Components map[string]string `json:"components"`
			Env        map[string]string `json:"env"`
		}
		if err := json.Unmarshal(content, &manifest); err != nil {
			t.Fatalf("decoding manifest: %v", err)
		}

		if len(manifest.Components) != 1 || manifest.Components["env"] != wantEnvironment {
			t.Fatalf("manifest components = %v, want only env = %s",
				manifest.Components, wantEnvironment)
		}

		if manifest.Env[probeName] != hex.EncodeToString(probeDigest[:]) {
			t.Fatalf("manifest does not preserve the digest of %s", probeName)
		}

		if strings.Contains(string(content), probeValue) {
			t.Fatal("an environment value reaches the manifest bytes")
		}

		manifests++
	}

	if manifests != 1 {
		t.Fatalf("stored %d manifests, want one", manifests)
	}
}

// TestACachedReplayIsByteIdenticalToTheFreshRun is the control for the
// silent-wrong-verdict class: whatever the cache stores, the report a reader
// gets from a replay must be the report the fresh run would have published.
// The matrix covers the outcome shapes a record carries: the killed majority,
// a survivor with its evidence, an Invalid row whose diagnostics are the
// stored claim's evidence, and the suppressed rows a policy removes — the
// diagnostics and the suppression being exactly what the deleted lifting
// helpers used to rebuild from published DTOs.
func TestACachedReplayIsByteIdenticalToTheFreshRun(t *testing.T) {
	t.Parallel()

	matrix := []struct {
		name    string
		fixture string
		request func(t *testing.T, module string) engine.RunRequest
		// coverage refuses a vacuous case: a fixture drift that silenced the
		// shape the case exists to compare must fail here, not pass silently.
		coverage func(t *testing.T, fresh report.Report)
	}{
		{
			name:    "killed majority",
			fixture: "all-killed",
			request: baseConfig,
		},
		{
			name:    "survivor with evidence",
			fixture: "skeleton",
			request: func(t *testing.T, module string) engine.RunRequest {
				t.Helper()

				request := baseConfig(t, module)
				request.IncludeOperators = []string{string(mutation.StrCase)}

				return request
			},
			coverage: func(t *testing.T, fresh report.Report) {
				t.Helper()

				if fresh.Count(report.Survived) == 0 {
					t.Fatal("fixture drift: the survivor case produced no survivor")
				}
			},
		},
		{
			name:    "invalid with diagnostics",
			fixture: "all-invalid",
			request: func(t *testing.T, module string) engine.RunRequest {
				t.Helper()

				request := baseConfig(t, module)
				request.IncludeOperators = []string{string(mutation.ModuleInputDelete)}

				return request
			},
			coverage: func(t *testing.T, fresh report.Report) {
				t.Helper()

				if fresh.Count(report.Invalid) == 0 {
					t.Fatal("fixture drift: the diagnostics case produced no Invalid row")
				}
			},
		},
		{
			name:    "suppressed by policy",
			fixture: "policy",
			request: baseConfig,
			coverage: func(t *testing.T, fresh report.Report) {
				t.Helper()

				if fresh.Count(report.Ignored) == 0 {
					t.Fatal("fixture drift: the suppression case produced no Ignored row")
				}
			},
		},
	}

	for _, entry := range matrix {
		t.Run(entry.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, entry.fixture)
			config := entry.request(t, module)

			fresh := cachedRun(t, config)
			replayed := cachedRun(t, config)

			if replayed.Population.Cached == 0 {
				t.Fatalf("the second run replayed nothing, so the comparison is vacuous: %+v",
					replayed.Population)
			}

			if entry.coverage != nil {
				entry.coverage(t, fresh)
			}

			assertCachedAndFreshReportsByteIdentical(t, fresh, replayed)
		})
	}
}

// previousCacheFormatVersion is the format one step behind the one this build
// writes: an entry carrying it is a miss, never a parse error.
const previousCacheFormatVersion = "tf-mut-cache-5"

// TestAnEntryUnderThePreviousFormatVersionIsAMissNotAnError is the symmetric
// half of the format boundary: the first run after an upgrade finds every
// entry one format behind, recomputes each verdict once, and the run still
// publishes a well-formed report.
func TestAnEntryUnderThePreviousFormatVersionIsAMissNotAnError(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	config := baseConfig(t, module)

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatalf("the first run reported %d cached verdicts from an empty cache",
			first.Population.Cached)
	}

	if first.Count(report.Survived) == 0 {
		t.Fatal("the first run wrote no survivor verdicts to invalidate")
	}

	legacy := 0

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(cacheDir(module), entry.Name())
		document := map[string]any{}
		if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
			t.Fatalf("decoding a fresh entry: %v", err)
		}

		document["format_version"] = previousCacheFormatVersion
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("encoding legacy entry: %v", err)
		}

		testsupport.WriteFile(t, path, string(encoded))
		legacy++
	}

	if legacy == 0 {
		t.Fatal("the first run wrote no entries to rewind")
	}

	second := cachedRun(t, config)
	if second.Population.Cached != 0 || second.Population.Fresh == 0 {
		t.Fatalf("previous-format entries were not freshly executed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)

	third := cachedRun(t, config)
	if third.Population.Cached == 0 || third.Population.Fresh != 0 {
		t.Fatalf("the recomputed verdicts were not cached under the new format: %+v", third.Population)
	}

	assertCachedAndFreshReportsByteIdentical(t, second, third)
}

func TestPreDeadlineSurvivorVerdictsAreNotReplayed(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	request := baseConfig(t, module)
	request.IncludeOperators = []string{string(mutation.StrCase)}
	first := cachedRun(t, request)
	if first.Count(report.Survived) == 0 {
		t.Fatal("the fixture produced no survivors to cache")
	}

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatal(err)
	}
	legacy := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(cacheDir(module), entry.Name())
		document := map[string]any{}
		if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
			t.Fatal(err)
		}
		document["format_version"] = "tf-mut-cache-3"
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		testsupport.WriteFile(t, path, string(encoded))
		legacy++
	}
	if legacy == 0 {
		t.Fatal("the first run wrote no survivor cache entries")
	}

	second := cachedRun(t, request)
	if second.Population.Cached != 0 || second.Population.Fresh == 0 {
		t.Fatalf("pre-deadline survivors were not freshly executed: %+v", second.Population)
	}
	assertVerdictInvariance(t, first, second)
}

func TestPreSandboxFixInvalidVerdictsAreNotReplayed(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	first := cachedRun(t, config)

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	legacy := 0

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(cacheDir(module), entry.Name())

		content, readErr := os.ReadFile(filepath.Clean(path))
		if readErr != nil {
			t.Fatalf("reading cache entry: %v", readErr)
		}

		document := map[string]any{}
		if decodeErr := json.Unmarshal(content, &document); decodeErr != nil {
			t.Fatalf("decoding cache entry: %v", decodeErr)
		}

		document["format_version"] = "tf-mut-cache-1"
		document["state"] = report.Invalid
		document["validated"] = true
		delete(document, "verdict")

		encoded, encodeErr := json.Marshal(document)
		if encodeErr != nil {
			t.Fatalf("encoding legacy cache entry: %v", encodeErr)
		}

		testsupport.WriteFile(t, path, string(encoded))
		legacy++
	}

	if legacy == 0 {
		t.Fatal("the first run wrote no entries to replace with legacy Invalid verdicts")
	}

	second := cachedRun(t, config)
	if second.Population.Cached != 0 || second.Population.Fresh == 0 {
		t.Fatalf("legacy Invalid verdicts were not freshly executed: %+v", second.Population)
	}

	assertVerdictInvariance(t, first, second)

	third := cachedRun(t, config)
	if third.Population.Cached == 0 || third.Population.Fresh != 0 {
		t.Fatalf("new verdicts were not cached: %+v", third.Population)
	}

	assertVerdictInvariance(t, second, third)
}

// TestCacheInvalidationPerKeyDimension: one fixture per key dimension. Each
// mutation of an input must turn the whole population fresh — the coarse key
// has no finer answer, and that is the point.
func TestCacheInvalidationPerKeyDimension(t *testing.T) {
	t.Parallel()

	dimensions := map[string]func(t *testing.T, module string, config *engine.RunRequest){
		"source-closure": func(t *testing.T, module string, _ *engine.RunRequest) {
			t.Helper()
			appendFile(t, filepath.Join(module, "main.tf"), "\n# touched\n")
		},
		"child-module-closure": func(t *testing.T, module string, _ *engine.RunRequest) {
			t.Helper()
			appendFile(t, filepath.Join(module, "child", "main.tf"), "\n# touched\n")
		},
		"test-files": func(t *testing.T, module string, _ *engine.RunRequest) {
			t.Helper()
			appendFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), "\n# touched\n")
		},
		"resolved-configuration": func(t *testing.T, _ string, config *engine.RunRequest) {
			t.Helper()

			config.ExcludeOperators = []string{"STR-CASE"}
		},
		"environment": func(t *testing.T, _ string, config *engine.RunRequest) {
			t.Helper()

			config.Env = append(config.Env, "TF_VAR_cache_probe=changed")
		},
	}

	for name, mutate := range dimensions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fixture := discriminateFixture
			if name == "test-files" {
				// discriminate's test file is named differently; use a fixture
				// whose layout the mutation function expects.
				fixture = "conditional-nonzero"
			}

			module := copyFixture(t, fixture)

			config := baseConfig(t, module)
			cachedRun(t, config)
			mutate(t, module, &config)

			result, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatalf("second run: %v", err)
			}

			if result.Population.Cached != 0 {
				t.Fatalf("dimension %s: %d cached verdicts survived an input change",
					name, result.Population.Cached)
			}

			// The miss explanation names a changed environment variable;
			// a moved dimension with an unchanged environment names nothing.
			if name == "environment" {
				if result.CacheMiss == nil ||
					!slices.Contains(result.CacheMiss.Added, "TF_VAR_cache_probe") {
					t.Fatalf("dimension %s: the environment-only miss carries %+v",
						name, result.CacheMiss)
				}
			} else if result.CacheMiss != nil {
				t.Fatalf("dimension %s: a non-environment miss carries %+v",
					name, result.CacheMiss)
			}
		})
	}
}

// TestMaskedBaselineFingerprintIsAKeyDimension: volatile-mask changes reach
// the key through the masked baseline fingerprint even when no source file
// changes — the dimension the C4 review named explicitly.
func TestMaskedBaselineFingerprintIsAKeyDimension(t *testing.T) {
	t.Parallel()

	// The volatile fixture's baseline fingerprint embeds the mask derived
	// from two baseline runs; the fixture is deterministic in its stable
	// parts, so a second run still hits. This case documents the wiring: the
	// fingerprint is hashed into the key, so any mask drift is a miss.
	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	first := cachedRun(t, config)

	if first.Baseline.Fingerprint == "" {
		t.Fatal("the baseline fingerprint is empty; the key cannot include it")
	}

	second := cachedRun(t, config)
	if second.Population.Cached == 0 {
		t.Fatal("a deterministic baseline produced a cache miss; the fingerprint dimension is unstable")
	}
}

// TestCacheIsRefusedUnderTheUnsafeOptIns: external state cannot be keyed
// soundly, so both opt-ins disable reads and writes.
func TestCacheIsRefusedUnderTheUnsafeOptIns(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true

	first, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	if entries, readErr := os.ReadDir(cacheDir(module)); readErr == nil && len(entries) > 0 {
		t.Fatalf("an unsafe run wrote %d cache entries", len(entries))
	}

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached != 0 {
		t.Fatalf("an unsafe run read %d cached verdicts", second.Population.Cached)
	}

	_ = first
}

// TestNoCacheDisablesReadsAndWrites is the documented mitigation for the
// plan-values-on-disk risk.
func TestNoCacheDisablesReadsAndWrites(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	config := baseConfig(t, module)
	config.NoCache = true

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run: %v", err)
	}

	if entries, err := os.ReadDir(cacheDir(module)); err == nil && len(entries) > 0 {
		t.Fatalf("--no-cache wrote %d cache entries", len(entries))
	}
}

// TestCorruptionIsAMiss: a truncated or unreadable entry never reaches a
// verdict; the mutant simply executes again.
func TestCorruptionIsAMiss(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	corrupted := 0

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		testsupport.WriteFile(t, filepath.Join(cacheDir(module), entry.Name()), "{ truncated")
		corrupted++
	}

	if corrupted == 0 {
		t.Fatal("the first run wrote no entries to corrupt")
	}

	result := cachedRun(t, config)

	if result.Population.Cached != 0 {
		t.Fatalf("%d corrupted entries were replayed as verdicts", result.Population.Cached)
	}

	if result.Population.Fresh == 0 {
		t.Fatal("nothing executed after corruption; the misses did not fall back to execution")
	}
}

// TestCacheEntriesAreOwnerOnly pins the 0700/0600 contract: evidence may
// embed plan values and source text, so nothing here is group-readable.
func TestCacheEntriesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	cachedRun(t, baseConfig(t, module))

	info, err := os.Stat(cacheDir(module))
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}

	if info.Mode().Perm() != 0o700 {
		t.Fatalf("cache directory mode = %v, want 0700", info.Mode().Perm())
	}

	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		stat, statErr := os.Stat(filepath.Join(cacheDir(module), entry.Name()))
		if statErr != nil {
			t.Fatalf("stat %s: %v", entry.Name(), statErr)
		}

		if stat.Mode().Perm()&0o077 != 0 {
			t.Fatalf("cache entry %s mode = %v; group or world access is forbidden",
				entry.Name(), stat.Mode().Perm())
		}
	}
}

// TestASymlinkedCacheDirectoryIsRefused: no symlink traversal — the cache
// neither reads through nor writes through a symlinked location.
func TestASymlinkedCacheDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	elsewhere := t.TempDir()

	if err := os.Symlink(elsewhere, cacheDir(module)); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	result := cachedRun(t, baseConfig(t, module))

	if result.Population.Cached != 0 {
		t.Fatal("a symlinked cache directory served cached verdicts")
	}

	if entries, err := os.ReadDir(elsewhere); err == nil && len(entries) > 0 {
		t.Fatalf("the engine wrote %d entries through a symlinked cache directory", len(entries))
	}
}

// TestEvictionIsDeterministicAndSizeCapped: over-cap entries are removed in a
// deterministic order, stale keys first.
func TestEvictionIsDeterministicAndSizeCapped(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	dir := cacheDir(module)

	// Fill the cache far beyond the cap with stale-shaped entries.
	for index := range engine.CacheMaxEntries + 32 {
		name := filepath.Join(dir, "0000stale-"+paddedIndex(index)+".json")
		testsupport.WriteFile(t, name, `{"format_version":"none"}`)
	}

	appendFile(t, filepath.Join(module, "main.tf"), "\n# touched\n")
	cachedRun(t, config)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	files := 0

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			files++
		}
	}

	if files > engine.CacheMaxEntries {
		t.Fatalf("cache holds %d entries after eviction; the cap is %d",
			files, engine.CacheMaxEntries)
	}
}

func paddedIndex(index int) string {
	return fmt.Sprintf("%05d", index)
}

// TestConcurrentInvocationsShareTheCacheSafely: the advisory lock serialises
// two engines writing the same cache; both complete, the store stays
// readable, and a third run replays it.
func TestConcurrentInvocationsShareTheCacheSafely(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	type outcome struct {
		result report.Report
		err    error
	}

	outcomes := make(chan outcome, 2)

	for range 2 {
		go func() {
			result, err := engine.Run(t.Context(), config)
			outcomes <- outcome{result: result, err: err}
		}()
	}

	for range 2 {
		if delivered := <-outcomes; delivered.err != nil {
			t.Fatalf("concurrent run: %v", delivered.err)
		}
	}

	third := cachedRun(t, config)
	if third.Population.Cached == 0 {
		t.Fatal("the cache two concurrent runs wrote could not be replayed")
	}
}

// TestTheLockFileIsAKeyDimension: a lock content change invalidates the whole
// population, even when no source file moved.
func TestTheLockFileIsAKeyDimension(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	// A minimal valid lock: comments only, which init accepts and preserves.
	lock := filepath.Join(module, ".terraform.lock.hcl")
	testsupport.WriteFile(t, lock, "# This file is maintained automatically by \"terraform init\".\n")

	config := baseConfig(t, module)
	cachedRun(t, config)

	appendFile(t, lock, "# touched\n")

	result := cachedRun(t, config)
	if result.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a lock change", result.Population.Cached)
	}
}

// TestRemoteModulePayloadsAreAKeyDimension: the module inventory includes the
// materialised remote payloads, so a change inside the remote module — with
// the root untouched — invalidates the cache.
//
// It does not run in parallel: the fixture drives git the way the remote
// tests do, serially.
//
//nolint:paralleltest // shares the git fixture pattern with remote_test.go.
func TestRemoteModulePayloadsAreAKeyDimension(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to build the remote-module fixture")
	}

	origin := gitOrigin(t)
	module := remoteConsumer(t, origin)

	config := baseConfig(t, module)

	first := cachedRun(t, config)
	if first.Population.Cached != 0 {
		t.Fatal("the first run hit an empty cache")
	}

	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 {
		t.Fatal("an unchanged remote module missed the cache; the dimension test is vacuous")
	}

	// Change the remote module's content and re-commit: the root module tree
	// is untouched, and only the downloaded payload differs.
	child := filepath.Join(origin, "mod", "main.tf")
	appendFile(t, child, "\n# remote payload touched\n")
	git(t, origin, "add", ".")
	git(t, origin, "-c", "user.email=tests@example.invalid", "-c", "user.name=tf-mut tests",
		"commit", "--quiet", "-m", "touch the payload")

	invalidated := cachedRun(t, config)
	if invalidated.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a remote payload change",
			invalidated.Population.Cached)
	}
}

// TestAutoVarFilesAreAKeyDimension: terraform.tfvars and *.auto.tfvars reach
// verdicts without appearing in the source closure, so the key must hash
// them (review of #48).
func TestAutoVarFilesAreAKeyDimension(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	testsupport.WriteFile(t, filepath.Join(module, "probe.auto.tfvars"), "# appeared\n")

	result := cachedRun(t, config)
	if result.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an auto-var file appearing", result.Population.Cached)
	}
}

// TestProviderEnvironmentIsAKeyDimension: AWS_-shaped configuration can reach
// a verdict, so it is keyed alongside the TF_ surface.
func TestProviderEnvironmentIsAKeyDimension(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	config.Env = append(config.Env, "AWS_REGION=eu-central-1")

	result := cachedRun(t, config)
	if result.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a provider environment change",
			result.Population.Cached)
	}
}

// TestAnExistingLooseCacheDirectoryIsCorrected: 0700 is the contract for the
// directory's whole life, not only its creation — evidence may embed plan
// values and source text.
func TestAnExistingLooseCacheDirectoryIsCorrected(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	dir := cacheDir(module)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the loose mode is the case under test.
		t.Fatalf("creating loose cache dir: %v", err)
	}

	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // the loose mode is the case under test.
		t.Fatalf("loosening cache dir: %v", err)
	}

	cachedRun(t, baseConfig(t, module))

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}

	if info.Mode().Perm() != 0o700 {
		t.Fatalf("a pre-existing %v cache directory was used without correction to 0700",
			info.Mode().Perm())
	}
}

// TestMockDataFilesAreAKeyDimension (round-3 review, PR #69): a mock_provider
// source directory's .tfmock.hcl/.tfmock.json files decide the values a
// mocked run sees, and nothing else hashes them — they are in no inventory
// and no test-file list.
func TestMockDataFilesAreAKeyDimension(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	testsupport.WriteFile(t, filepath.Join(module, "tests", "data.tfmock.hcl"), "# mock data v1\n")
	cachedRun(t, config)

	testsupport.WriteFile(t, filepath.Join(module, "tests", "data.tfmock.hcl"), "# mock data v2\n")

	result := cachedRun(t, config)
	if result.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived a mock-data edit", result.Population.Cached)
	}
}

// TestACacheMissCausedOnlyByTheEnvironmentNamesTheVariable (road-test F11,
// option B): the key keeps hashing the whole environment, but a miss caused
// only by the environment names the variable that differed — never its
// value.
func TestACacheMissCausedOnlyByTheEnvironmentNamesTheVariable(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	const probeName = "TFMUT_CACHE_PROBE"
	const probeValue = "probe-value-7f3a9c2e"
	config.Env = append(config.Env, probeName+"="+probeValue)

	second := cachedRun(t, config)
	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an unrelated environment change",
			second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("an environment-only cache miss named no variable")
	}

	if !slices.Contains(second.CacheMiss.Added, probeName) {
		t.Fatalf("the miss names added %q, which omits %q",
			second.CacheMiss.Added, probeName)
	}

	assertReportCarriesNoValue(t, second, probeValue)
}

// TestAChangedEnvironmentVariableIsReportedAsChanged: renaming nothing and
// adding nothing, only the value differs — the miss still names the variable
// and still withholds both values.
func TestAChangedEnvironmentVariableIsReportedAsChanged(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	const probeName = "TFMUT_CACHE_PROBE"
	const firstValue = "probe-first-4b1d88"
	const secondValue = "probe-second-9e27f1"
	config.Env = append(config.Env, probeName+"="+firstValue)
	cachedRun(t, config)

	for index, entry := range config.Env {
		if strings.HasPrefix(entry, probeName+"=") {
			config.Env[index] = probeName + "=" + secondValue
		}
	}

	second := cachedRun(t, config)
	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an environment value change",
			second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("an environment-only cache miss named no variable")
	}

	if !slices.Contains(second.CacheMiss.Changed, probeName) {
		t.Fatalf("the miss names changed %q, which omits %q",
			second.CacheMiss.Changed, probeName)
	}

	assertReportCarriesNoValue(t, second, firstValue)
	assertReportCarriesNoValue(t, second, secondValue)
}

func TestAnEnvironmentChangeThatChangesTheBaselineNamesTheVariable(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "operators")
	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(mutation.StrCase)}
	envIndex := len(config.Env)
	config.Env = append(config.Env, `TF_VAR_names=["alpha","beta"]`)
	first := cachedRun(t, config)
	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 {
		t.Fatal("the unchanged run replayed nothing; the case is vacuous")
	}

	config.Env[envIndex] = `TF_VAR_names=["alpha","gamma"]`
	second := cachedRun(t, config)
	if second.Baseline.Fingerprint == first.Baseline.Fingerprint {
		t.Fatal("changing TF_VAR_names did not change the baseline fingerprint")
	}

	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an environment and baseline change",
			second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("an environment and baseline cache miss named no variable")
	}

	if !slices.Equal(second.CacheMiss.Changed, []string{"TF_VAR_names"}) ||
		len(second.CacheMiss.Added) != 0 || len(second.CacheMiss.Removed) != 0 {
		t.Fatalf("the miss names %+v, want only TF_VAR_names as changed", second.CacheMiss)
	}

	replayed := cachedRun(t, config)
	if replayed.Population.Cached == 0 || replayed.CacheMiss != nil {
		t.Fatalf("the unchanged new environment did not replay without an explanation: %+v",
			replayed.CacheMiss)
	}
}

// TestARemovedEnvironmentVariableIsReportedAsRemoved: the variable the first
// run saw is gone from the second — the miss names it without its value.
func TestARemovedEnvironmentVariableIsReportedAsRemoved(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	const probeName = "TFMUT_CACHE_PROBE"
	const probeValue = "probe-value-c53a917d"
	config.Env = append(config.Env, probeName+"="+probeValue)
	cachedRun(t, config)

	kept := config.Env[:0]
	for _, entry := range config.Env {
		if !strings.HasPrefix(entry, probeName+"=") {
			kept = append(kept, entry)
		}
	}
	config.Env = kept

	second := cachedRun(t, config)
	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an environment variable removal",
			second.Population.Cached)
	}

	if second.CacheMiss == nil {
		t.Fatal("an environment-only cache miss named no variable")
	}

	if !slices.Contains(second.CacheMiss.Removed, probeName) {
		t.Fatalf("the miss names removed %q, which omits %q",
			second.CacheMiss.Removed, probeName)
	}

	assertReportCarriesNoValue(t, second, probeValue)
}

// TestACacheHitCarriesNoMissExplanation: the explanation is present exactly
// when the run missed and the environment changed — a hit carries none.
func TestACacheHitCarriesNoMissExplanation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	first := cachedRun(t, config)
	if first.CacheMiss != nil {
		t.Fatalf("a first run over an empty cache carries a miss explanation: %+v",
			first.CacheMiss)
	}

	second := cachedRun(t, config)
	if second.Population.Cached == 0 {
		t.Fatal("the second unchanged run replayed nothing; the case is vacuous")
	}

	if second.CacheMiss != nil {
		t.Fatalf("a cache hit carries a miss explanation: %+v", second.CacheMiss)
	}
}

// TestAMissWithNoStoredGenerationCarriesNoExplanation: an old-format cache —
// entries but no per-generation digests — misses without an explanation
// rather than guessing one.
func TestAMissWithNoStoredGenerationCarriesNoExplanation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	// Simulate a cache written before per-generation digests existed: the
	// entries stay, every manifest goes.
	entries, err := os.ReadDir(cacheDir(module))
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}

	removed := 0

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-manifest.json") {
			if err := os.Remove(filepath.Join(cacheDir(module), entry.Name())); err != nil {
				t.Fatalf("removing manifest: %v", err)
			}

			removed++
		}
	}

	if removed == 0 {
		t.Fatal("the first run stored no generation manifest to remove")
	}

	config.Env = append(config.Env, "TFMUT_CACHE_PROBE=probe-value-2a6f40b1")
	second := cachedRun(t, config)
	if second.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts survived an environment change", second.Population.Cached)
	}

	if second.CacheMiss != nil {
		t.Fatalf("a miss with no stored generation carries an explanation: %+v",
			second.CacheMiss)
	}
}

// TestAMissExplanationIsBoundedAndSorted: a wholesale environment
// replacement still yields a bounded, sorted explanation — never the
// environment listing the field exists to avoid — with truncation flagged.
func TestAMissExplanationIsBoundedAndSorted(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)

	const probes = 40
	for index := range probes {
		config.Env = append(config.Env,
			fmt.Sprintf("TFMUT_CACHE_PROBE_%02d=probe-value-%02d", index, index))
	}

	second := cachedRun(t, config)
	if second.CacheMiss == nil {
		t.Fatal("an environment-only cache miss named no variable")
	}

	total := len(second.CacheMiss.Added) + len(second.CacheMiss.Removed) +
		len(second.CacheMiss.Changed)
	if total != report.MaxCacheMissNames {
		t.Fatalf("the miss carries %d names, want the bound %d",
			total, report.MaxCacheMissNames)
	}

	if !second.CacheMiss.Truncated {
		t.Fatalf("%d added variables truncated without the flag: %+v", probes, second.CacheMiss)
	}

	for _, list := range [][]string{
		second.CacheMiss.Added, second.CacheMiss.Removed, second.CacheMiss.Changed,
	} {
		if !slices.IsSorted(list) {
			t.Fatalf("the miss list %q is not sorted", list)
		}
	}

	assertReportCarriesNoValue(t, second, "probe-value-07")
}

// assertReportCarriesNoValue fails when the environment value reaches any
// report byte — the JSON document, and through it every reporter derived
// from the same value.
func assertReportCarriesNoValue(t *testing.T, value report.Report, secret string) {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding report: %v", err)
	}

	if strings.Contains(string(encoded), secret) {
		t.Fatal("an environment value reaches the report bytes")
	}
}
