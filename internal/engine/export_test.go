package engine

import (
	"fmt"
	"sync"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

var characteriseWriteSeedMu sync.Mutex

// SetCharacteriseWriteSeeds exposes only the five characterisation-write
// controls to the external test package. The module filter keeps unrelated
// parallel engine-seam tests on the inert defaults; the lock serialises the
// tests that mutate these package-level hooks.
func SetCharacteriseWriteSeeds(
	t *testing.T,
	moduleDir, closureChange, closureFile string,
	closureAfter int,
	renameWindowChange, registryFailure bool,
) {
	t.Helper()
	characteriseWriteSeedMu.Lock()
	t.Cleanup(characteriseWriteSeedMu.Unlock)

	seedClosureChange = func(configuration discovery.Configuration, _ string) error {
		if configuration.ModuleDir != moduleDir || closureChange == "" {
			return nil
		}
		return stageClosureChange(configuration, closureChange)
	}
	seedClosureFile = func(configuration discovery.Configuration, _ string) error {
		if configuration.ModuleDir != moduleDir || closureFile == "" {
			return nil
		}
		return stageClosureFile(configuration, closureFile)
	}
	seedClosureAfter = func(configuration discovery.Configuration, written int) error {
		if configuration.ModuleDir != moduleDir || written != closureAfter {
			return nil
		}
		if closureFile != "" {
			if err := seedClosureFile(configuration, closureFile); err != nil {
				return err
			}
		}
		if closureChange != "" {
			return seedClosureChange(configuration, closureChange)
		}
		return nil
	}
	seedRenameWindowChange = func(configuration discovery.Configuration, calls int) error {
		if !renameWindowChange || configuration.ModuleDir != moduleDir || calls != insideTheWindow {
			return nil
		}
		return seedClosureChange(configuration, closureChange)
	}
	seedRegistryFailure = func(currentModuleDir string) error {
		if !registryFailure || currentModuleDir != moduleDir {
			return nil
		}
		return fmt.Errorf("%w: the provenance registry could not be stored", ErrWriteRefused)
	}
	t.Cleanup(func() {
		seedClosureChange = func(discovery.Configuration, string) error { return nil }
		seedClosureFile = func(discovery.Configuration, string) error { return nil }
		seedClosureAfter = func(discovery.Configuration, int) error { return nil }
		seedRenameWindowChange = func(discovery.Configuration, int) error { return nil }
		seedRegistryFailure = func(string) error { return nil }
	})
}
