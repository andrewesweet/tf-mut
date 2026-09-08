package engine

import (
	"fmt"
	"slices"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// SetMissingMockSeed removes one rendered provider-configuration mock for one
// module. Its callers are sequential for the hook's complete lifetime.
func SetMissingMockSeed(t *testing.T, moduleDir, missingConfiguration string) {
	t.Helper()
	seedMissingMock = func(configuration discovery.Configuration,
		staged characterise.Scaffold,
	) characterise.Scaffold {
		if configuration.ModuleDir != moduleDir || missingConfiguration == "" {
			return staged
		}

		kept := slices.DeleteFunc(slices.Clone(staged.Mocks), func(mock characterise.Mock) bool {
			return configurationName(discovery.ProviderAlias{Name: mock.Name, Alias: mock.Alias}) ==
				missingConfiguration
		})
		staged.Mocks = kept

		return staged
	}
	t.Cleanup(func() {
		seedMissingMock = func(_ discovery.Configuration,
			staged characterise.Scaffold,
		) characterise.Scaffold {
			return staged
		}
	})
}

// SetFinalPinDefectSeed adds one knowingly false pin for one module. Its caller
// is sequential for the hook's complete lifetime.
func SetFinalPinDefectSeed(t *testing.T, moduleDir string) {
	t.Helper()
	seedFinalPinDefect = func(configuration discovery.Configuration, pins []report.Pin) []report.Pin {
		if configuration.ModuleDir != moduleDir || len(pins) == 0 {
			return pins
		}

		defect := pins[0]
		defect.ID = characterise.PinID(defect.Scenario, defect.Address, "seeded")
		defect.Expression = defect.Address + ` == "tf-mut-seeded-final-pin-defect"`

		return append(slices.Clone(pins), defect)
	}
	t.Cleanup(func() {
		seedFinalPinDefect = func(_ discovery.Configuration, pins []report.Pin) []report.Pin {
			return pins
		}
	})
}

// SetInitialPinDefectSeed adds one knowingly false pin for one module. Its caller
// is sequential for the hook's complete lifetime.
func SetInitialPinDefectSeed(t *testing.T, moduleDir string) {
	t.Helper()
	seedInitialPinDefect = func(configuration discovery.Configuration, pins []report.Pin) []report.Pin {
		if configuration.ModuleDir != moduleDir || len(pins) == 0 {
			return pins
		}

		defect := pins[0]
		defect.ID = characterise.PinID(defect.Scenario, defect.Address, "seeded-initial")
		defect.Expression = defect.Address + ` == "tf-mut-seeded-initial-pin-defect"`

		return append(slices.Clone(pins), defect)
	}
	t.Cleanup(func() {
		seedInitialPinDefect = func(_ discovery.Configuration, pins []report.Pin) []report.Pin {
			return pins
		}
	})
}

// SetNoEscalationSeed suppresses escalation for one module. Its caller is
// sequential for the hook's complete lifetime.
func SetNoEscalationSeed(t *testing.T, moduleDir string) {
	t.Helper()
	seedNoEscalation = func(configuration discovery.Configuration,
		scaffold characterise.Scaffold,
	) characterise.Scaffold {
		if configuration.ModuleDir != moduleDir {
			return scaffold
		}

		scaffold.Rung = scaffold.Requested
		scaffold.Escalated = false
		scaffold.EscalationReason = ""

		return scaffold
	}
	t.Cleanup(func() {
		seedNoEscalation = func(_ discovery.Configuration,
			scaffold characterise.Scaffold,
		) characterise.Scaffold {
			return scaffold
		}
	})
}

// SetCharacteriseWriteSeeds exposes only the five characterisation-write
// controls to the external test package. Its callers are deliberately
// sequential: the hooks are package globals, so their complete lifetime must
// not overlap any other engine-seam test.
//
//nolint:revive // the test-only setter intentionally wires five independent controls.
func SetCharacteriseWriteSeeds(
	t *testing.T,
	moduleDir, closureChange, closureFile string,
	closureAfter int,
	renameWindowChange, registryFailure bool,
) {
	t.Helper()
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
		if configuration.ModuleDir != moduleDir || written != closureAfter || renameWindowChange {
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
