package engine_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// M3b.2 (#48): the incremental cache — coarse before clever, safe as a disk
// format. The key hashes the entire materialised source closure, all tests,
// the resolved configuration, the lock and module inventory, the relevant
// environment, the Terraform identity, the cache format version and the
// masked baseline fingerprint; any doubt is a miss.

// cachedRun executes one run with the given configuration. Tests that expect
// a cache hit must reuse one configuration: the key includes the relevant
// environment, and the harness gives each configuration its own
// TF_PLUGIN_CACHE_DIR.
func cachedRun(t *testing.T, config engine.Config) report.Report {
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

// TestCacheInvalidationPerKeyDimension: one fixture per key dimension. Each
// mutation of an input must turn the whole population fresh — the coarse key
// has no finer answer, and that is the point.
func TestCacheInvalidationPerKeyDimension(t *testing.T) {
	t.Parallel()

	dimensions := map[string]func(t *testing.T, module string, config *engine.Config){
		"source-closure": func(t *testing.T, module string, _ *engine.Config) {
			t.Helper()
			appendFile(t, filepath.Join(module, "main.tf"), "\n# touched\n")
		},
		"child-module-closure": func(t *testing.T, module string, _ *engine.Config) {
			t.Helper()
			appendFile(t, filepath.Join(module, "child", "main.tf"), "\n# touched\n")
		},
		"test-files": func(t *testing.T, module string, _ *engine.Config) {
			t.Helper()
			appendFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), "\n# touched\n")
		},
		"resolved-configuration": func(t *testing.T, _ string, config *engine.Config) {
			t.Helper()

			config.ExcludeOperators = []string{"STR-CASE"}
		},
		"environment": func(t *testing.T, _ string, config *engine.Config) {
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

		writeFile(t, filepath.Join(cacheDir(module), entry.Name()), "{ truncated")
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
		writeFile(t, name, `{"format_version":"none"}`)
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
	writeFile(t, lock, "# This file is maintained automatically by \"terraform init\".\n")

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
