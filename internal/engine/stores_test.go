package engine_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/store"
)

// errNoStore is the wiring failure the injection test stages.
var errNoStore = errors.New("no store here")

// TestTheVerdictStoreIsOpenedThroughTheDependencies pins the store port's
// wiring: a run consults Dependencies.NewVerdictStore once, at the
// module-local cache directory, and a store that cannot open costs the run
// its cache — not its verdicts.
func TestTheVerdictStoreIsOpenedThroughTheDependencies(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	opened := []string{}
	dependencies := engine.DefaultDependencies()
	dependencies.NewVerdictStore = func(dir string) (engine.VerdictStore, error) {
		opened = append(opened, dir)

		// A store that never recognises its format is the cache-disabled
		// behaviour for every read, driven through the port itself.
		return store.NewVerdictCache(dir, "test-unopenable", "test-unopenable", engine.CacheMaxEntries)
	}

	result, err := engine.New(dependencies).Run(t.Context(), &config)
	if err != nil {
		t.Fatalf("the run failed: %v", err)
	}

	want := filepath.Join(module, ".tf-mut-cache")
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("the run opened the verdict store at %v, want [%s]", opened, want)
	}

	if result.Population.Cached != 0 || result.Population.Fresh == 0 {
		t.Fatalf("the run was not freshly executed beside the port: %+v", result.Population)
	}
}

// TestAVerdictStoreThatCannotOpenDisablesTheCache pins the failure
// direction: a store the wiring cannot open is a run without a cache, never
// a failed run.
func TestAVerdictStoreThatCannotOpenDisablesTheCache(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	config := baseConfig(t, module)

	dependencies := engine.DefaultDependencies()
	dependencies.NewVerdictStore = func(string) (engine.VerdictStore, error) {
		return nil, errNoStore
	}

	result, err := engine.New(dependencies).Run(t.Context(), &config)
	if err != nil {
		t.Fatalf("an unopenable verdict store failed the run: %v", err)
	}

	if result.Population.Cached != 0 || result.Population.Fresh == 0 {
		t.Fatalf("the run was not freshly executed without the store: %+v", result.Population)
	}
}
