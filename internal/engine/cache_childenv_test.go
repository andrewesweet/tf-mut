package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
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
