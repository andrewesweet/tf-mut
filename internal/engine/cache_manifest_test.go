package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

const (
	emptyCacheNameDefect    = "empty-name"
	equalsCacheNameDefect   = "equals-in-name"
	nulCacheNameDefect      = "nul-in-name"
	invalidCacheValueDefect = "invalid-value-digest"
	invalidCacheDigest      = "not-a-digest"
)

func TestNonregularCacheManifestsAreNotRead(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"symlink", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "all-killed")
			config := baseConfig(t, module)
			cachedRun(t, config)
			path := onlyCacheManifest(t, module)
			payload := filepath.Join(cacheDir(module), "manifest-payload")
			if err := os.Rename(path, payload); err != nil {
				t.Fatalf("moving manifest: %v", err)
			}

			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(payload, path)
			case "fifo":
				err = syscall.Mkfifo(path, 0o600)
			case "directory":
				err = os.Mkdir(path, 0o700)
			default:
				t.Fatalf("unknown nonregular manifest kind %q", kind)
			}

			if err != nil {
				t.Fatalf("creating %s manifest: %v", kind, err)
			}

			config.Env = append(config.Env, "TFMUT_CACHE_PROBE=nonregular-probe")
			result := cachedRun(t, config)
			if result.Population.Cached != 0 || result.CacheMiss != nil {
				t.Fatalf("a %s manifest served cache evidence: %+v", kind, result.CacheMiss)
			}
		})
	}
}

func TestEnvironmentOverridesKeepEveryValueInTheNameDigest(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	index := len(config.Env)
	config.Env = append(config.Env, "AWS_REGION=eu-central-1")
	cachedRun(t, config)

	config.Env[index] = "AWS_REGION=eu-west-1"
	changed := cachedRun(t, config)
	assertOnlyChangedEnvironmentName(t, changed, "AWS_REGION")

	config.Env = config.Env[:index]
	removedOverride := cachedRun(t, config)
	assertOnlyChangedEnvironmentName(t, removedOverride, "AWS_REGION")

	for _, value := range []string{"us-west-2", "eu-central-1", "eu-west-1"} {
		assertReportCarriesNoValue(t, changed, value)
		assertReportCarriesNoValue(t, removedOverride, value)
	}

	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 || warm.CacheMiss != nil {
		t.Fatal("the unchanged environment did not replay silently")
	}
}

func TestStaticPopulationsCarryNoCacheMissExplanation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "suggest-count-zero")
	testsupport.WriteFile(t, filepath.Join(module, mainFile), `resource "terraform_data" "zeroed" {
  count = 0
  input = "steady"
}
`)
	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(mutation.StrCase)}
	first := cachedRun(t, config)
	if len(first.Mutants) == 0 || first.Count(report.NoCoverage) != len(first.Mutants) {
		t.Fatal("the fixture did not produce a non-empty static population")
	}

	config.Env = append(config.Env, "TFMUT_CACHE_PROBE=static-probe")
	second := cachedRun(t, config)
	if second.CacheMiss != nil {
		t.Fatalf("a population with no pending lookup carries %+v", second.CacheMiss)
	}
}

func TestDisjointSamplesCarryNoCacheMissExplanation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	appendFile(t, filepath.Join(module, mainFile), "\noutput \"echo\" { value = \"steady\" }\n")
	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(mutation.StrCase)}
	config.HasSample = true
	config.SamplePercent = 1
	first := cachedRun(t, config)
	if len(first.Mutants) != 1 || first.Mutants[0].ExecutedRuns == 0 {
		t.Fatal("the first sample did not execute exactly one mutant")
	}

	warm := cachedRun(t, config)
	if warm.Population.Cached != 1 {
		t.Fatal("the first sample stored no reusable verdict")
	}

	found := false
	for seed := int64(1); seed <= 32; seed++ {
		request := engine.PreviewRequest{Common: config.Common, Population: config.Population}
		request.SampleSeed = seed
		previewed, err := engine.Run(t.Context(), request)
		if err != nil {
			t.Fatalf("previewing sample: %v", err)
		}

		if len(previewed.Mutants) == 1 && previewed.Mutants[0].ID != first.Mutants[0].ID {
			config.SampleSeed = seed
			found = true

			break
		}
	}

	if !found {
		t.Fatal("the fixture produced no disjoint sample")
	}

	config.Env = append(config.Env, "TFMUT_CACHE_PROBE=sample-probe")
	second := cachedRun(t, config)
	if second.Population.Cached != 0 || second.CacheMiss != nil || second.Mutants[0].ExecutedRuns == 0 {
		t.Fatalf("a sample with no prior entry carries cache evidence: %+v", second.CacheMiss)
	}
}

func TestCacheMissExplanationsRequireUsableVerdictEntries(t *testing.T) {
	t.Parallel()

	for _, defect := range []string{"missing", "truncated", "illegal-record", "static-record", "wrong-key"} {
		t.Run(defect, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "all-killed")
			config := baseConfig(t, module)
			cachedRun(t, config)
			entries := cacheVerdictFiles(t, module)
			if len(entries) == 0 {
				t.Fatal("the first run wrote no verdict entries")
			}

			for _, path := range entries {
				corruptCacheVerdict(t, path, defect)
			}

			config.Env = append(config.Env, "TFMUT_CACHE_PROBE=entry-probe")
			second := cachedRun(t, config)
			if second.CacheMiss != nil || second.Population.Cached != 0 || second.Population.Fresh == 0 {
				t.Fatalf("%s entries served cache evidence: %+v", defect, second.CacheMiss)
			}
		})
	}
}

func TestTheNewestMatchingEnvironmentStopsCacheMissAttribution(t *testing.T) {
	t.Parallel()

	for _, change := range []string{"source", "missing-current-entries"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "all-killed")
			config := baseConfig(t, module)
			index := len(config.Env)
			config.Env = append(config.Env, "TFMUT_CACHE_PROBE=old-probe")
			cachedRun(t, config)
			oldPath := onlyCacheManifest(t, module)
			if err := os.Chtimes(oldPath, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
				t.Fatalf("dating old manifest: %v", err)
			}

			config.Env[index] = "TFMUT_CACHE_PROBE=new-probe"
			changed := cachedRun(t, config)
			assertOnlyChangedEnvironmentName(t, changed, "TFMUT_CACHE_PROBE")
			paths := cacheManifestFiles(t, module)
			if len(paths) != 2 {
				t.Fatal("the environment change did not store a second generation")
			}

			if change == "source" {
				appendFile(t, filepath.Join(module, mainFile), "\n# source-only edit\n")
			} else {
				removeNewerCacheVerdicts(t, module, paths, oldPath)
			}

			third := cachedRun(t, config)
			if third.Population.Cached != 0 || third.CacheMiss != nil {
				t.Fatalf("%s was attributed to an obsolete environment: %+v", change, third.CacheMiss)
			}
		})
	}
}

func TestMalformedCacheManifestsCarryNoExplanation(t *testing.T) {
	t.Parallel()

	for _, defect := range []string{
		"renamed-env", "null-env", "empty-env", emptyCacheNameDefect,
		equalsCacheNameDefect, nulCacheNameDefect, invalidCacheValueDefect,
		"missing-key", "invalid-key", "wrong-filename", "missing-components", "missing-env-component",
		"invalid-env-digest", "extra-component", "wrong-format",
	} {
		t.Run(defect, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "all-killed")
			config := baseConfig(t, module)
			cachedRun(t, config)
			path := onlyCacheManifest(t, module)
			document := readCacheDocument(t, path)

			switch defect {
			case "renamed-env":
				document["enw"] = document["env"]
				delete(document, "env")
			case "null-env":
				document["env"] = nil
			case "empty-env":
				document["env"] = map[string]any{}
			case emptyCacheNameDefect, equalsCacheNameDefect, nulCacheNameDefect, invalidCacheValueDefect:
				names := map[string]string{
					emptyCacheNameDefect:    "",
					equalsCacheNameDefect:   "A=B",
					nulCacheNameDefect:      "A\x00B",
					invalidCacheValueDefect: "A",
				}
				digest := strings.Repeat("a", 64)
				if defect == invalidCacheValueDefect {
					digest = invalidCacheDigest
				}

				cacheDocumentObject(t, document, "env")[names[defect]] = digest
			case "missing-key":
				delete(document, "run_key")
			case "invalid-key":
				document["run_key"] = invalidCacheDigest
			case "wrong-filename":
				document["run_key"] = strings.Repeat("0", 64)
			case "missing-components":
				delete(document, "components")
			case "missing-env-component":
				delete(cacheDocumentObject(t, document, "components"), "env")
			case "invalid-env-digest":
				cacheDocumentObject(t, document, "components")["env"] = invalidCacheDigest
			case "extra-component":
				cacheDocumentObject(t, document, "components")["source"] = strings.Repeat("a", 64)
			case "wrong-format":
				document["format_version"] = "not-this-format"
			default:
				t.Fatalf("unknown manifest defect %q", defect)
			}

			writeCacheDocument(t, path, document)
			config.Env = append(config.Env, "TFMUT_CACHE_PROBE=manifest-probe")
			second := cachedRun(t, config)
			if second.Population.Cached != 0 || second.CacheMiss != nil {
				t.Fatalf("a %s manifest served cache evidence: %+v", defect, second.CacheMiss)
			}
		})
	}
}

func TestOlderNameDigestManifestsDoNotInvalidateVerdicts(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)
	cachedRun(t, config)
	path := onlyCacheManifest(t, module)
	document := readCacheDocument(t, path)
	document["format_version"] = "tf-mut-cache-5"
	writeCacheDocument(t, path, document)
	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 || warm.CacheMiss != nil {
		t.Fatal("the manifest format revision invalidated verdict entries")
	}

	writeCacheDocument(t, path, document)
	config.Env = append(config.Env, "TFMUT_CACHE_PROBE=legacy-manifest-probe")
	second := cachedRun(t, config)
	if second.Population.Cached != 0 || second.CacheMiss != nil {
		t.Fatalf("an older name-digest encoding served cache evidence: %+v", second.CacheMiss)
	}
}

func assertOnlyChangedEnvironmentName(t *testing.T, value report.Report, name string) {
	t.Helper()

	if value.Population.Cached != 0 || value.CacheMiss == nil ||
		!slices.Equal(value.CacheMiss.Changed, []string{name}) ||
		len(value.CacheMiss.Added)+len(value.CacheMiss.Removed) != 0 {
		t.Fatalf("the cache miss carries %+v, want only %s as changed", value.CacheMiss, name)
	}
}

func cacheManifestFiles(t *testing.T, module string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(cacheDir(module), "*-manifest.json"))
	if err != nil {
		t.Fatalf("listing manifests: %v", err)
	}

	return paths
}

func onlyCacheManifest(t *testing.T, module string) string {
	t.Helper()

	paths := cacheManifestFiles(t, module)
	if len(paths) != 1 {
		t.Fatalf("found %d manifests, want one", len(paths))
	}

	return paths[0]
}

func cacheVerdictFiles(t *testing.T, module string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(cacheDir(module), "*.json"))
	if err != nil {
		t.Fatalf("listing verdict entries: %v", err)
	}

	return slices.DeleteFunc(paths, func(path string) bool {
		return strings.HasSuffix(path, "-manifest.json")
	})
}

func corruptCacheVerdict(t *testing.T, path, defect string) {
	t.Helper()

	switch defect {
	case "missing":
		if err := os.Remove(path); err != nil {
			t.Fatalf("removing entry: %v", err)
		}

		return
	case "truncated":
		testsupport.WriteFile(t, path, "{ truncated")

		return
	default:
	}

	document := readCacheDocument(t, path)
	switch defect {
	case "wrong-key":
		document["run_key"] = strings.Repeat("0", 64)
	case "static-record":
		document["record"] = map[string]any{"state": "NoCoverage", "message": "static"}
	case "illegal-record":
		document["record"] = map[string]any{"state": "not-a-verdict"}
	default:
		t.Fatalf("unknown verdict defect %q", defect)
	}

	writeCacheDocument(t, path, document)
}

func removeNewerCacheVerdicts(t *testing.T, module string, manifests []string, oldPath string) {
	t.Helper()

	for _, path := range manifests {
		if path == oldPath {
			continue
		}

		prefix := strings.TrimSuffix(filepath.Base(path), "-manifest.json") + "-"
		for _, entry := range cacheVerdictFiles(t, module) {
			if !strings.HasPrefix(filepath.Base(entry), prefix) {
				continue
			}

			if err := os.Remove(entry); err != nil {
				t.Fatalf("removing current entry: %v", err)
			}
		}
	}
}

func cacheDocumentObject(t *testing.T, document map[string]any, key string) map[string]any {
	t.Helper()

	object, ok := document[key].(map[string]any)
	if !ok {
		t.Fatalf("cache document member %q is not an object", key)
	}

	return object
}

func readCacheDocument(t *testing.T, path string) map[string]any {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // Paths come from fixture copies under t.TempDir().
	if err != nil {
		t.Fatalf("reading cache document: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("decoding cache document: %v", err)
	}

	return document
}

func writeCacheDocument(t *testing.T, path string, document map[string]any) {
	t.Helper()

	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding cache document: %v", err)
	}

	testsupport.WriteFile(t, path, string(content))
}
