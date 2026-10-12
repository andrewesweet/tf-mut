package engine

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// SetGenerationDefectSeed makes generation perform one known defect for one
// module: the red proofs for the origins gate cases. Its callers are
// sequential for the hook's complete lifetime.
func SetGenerationDefectSeed(t *testing.T, moduleDir string, defect mutation.Defect) {
	t.Helper()
	seedGenerationDefect = func(settings settings) mutation.Defect {
		if settings.ModuleDir != moduleDir {
			return mutation.DefectNone
		}

		return defect
	}
	t.Cleanup(func() {
		seedGenerationDefect = func(settings) mutation.Defect { return mutation.DefectNone }
	})
}

// SetStaticShortcutsDisabled makes one module classify every mutant by execution.
// Its callers are sequential for the hook's complete lifetime.
func SetStaticShortcutsDisabled(t *testing.T, moduleDir string) {
	t.Helper()
	disableStaticShortcuts = func(settings settings) bool {
		return settings.ModuleDir == moduleDir
	}
	t.Cleanup(func() {
		disableStaticShortcuts = func(settings) bool { return false }
	})
}

// SetJSONReadingDisabled leaves JSON-syntax files unread for one module.
// Its callers are sequential for the hook's complete lifetime.
func SetJSONReadingDisabled(t *testing.T, moduleDir string) {
	t.Helper()
	disableJSONReading = func(settings settings) bool {
		return settings.ModuleDir == moduleDir
	}
	t.Cleanup(func() {
		disableJSONReading = func(settings) bool { return false }
	})
}

// SetSuggestionDefectSeed makes suggestion generation emit one known defect
// for one module. Its callers are sequential for the hook's complete lifetime.
func SetSuggestionDefectSeed(t *testing.T, moduleDir string, defect suggest.Defect) {
	t.Helper()
	seedSuggestionDefect = func(settings settings) suggest.Defect {
		if settings.ModuleDir != moduleDir {
			return suggest.DefectNone
		}

		return defect
	}
	t.Cleanup(func() {
		seedSuggestionDefect = func(settings) suggest.Defect { return suggest.DefectNone }
	})
}

// SetMissingMockSeed removes one rendered provider-configuration mock for one
// module. Its callers are sequential for the hook's complete lifetime.
func SetMissingMockSeed(t *testing.T, moduleDir, missingConfiguration string) {
	t.Helper()
	seedMissingMock = func(configuration discovery.Configuration,
		staged characterise.SuitePlan,
	) characterise.SuitePlan {
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
			staged characterise.SuitePlan,
		) characterise.SuitePlan {
			return staged
		}
	})
}

// SetProgressInterval overrides the progress cadence for one test. Its
// callers are sequential for the seam's complete lifetime.
func SetProgressInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	progressInterval = interval
	t.Cleanup(func() {
		progressInterval = DefaultProgressInterval
	})
}

// SetUntilDryRounds bounds the until-dry loop for one module. Its callers are
// sequential for the hook's complete lifetime.
func SetUntilDryRounds(t *testing.T, moduleDir string, rounds int) {
	t.Helper()
	seedUntilDryRounds = func(settings settings) int {
		if settings.ModuleDir != moduleDir {
			return 0
		}

		return rounds
	}
	t.Cleanup(func() {
		seedUntilDryRounds = func(settings) int { return 0 }
	})
}

// SetNoEscalationSeed suppresses escalation for one module. Its caller is
// sequential for the hook's complete lifetime.
func SetNoEscalationSeed(t *testing.T, moduleDir string) {
	t.Helper()
	seedNoEscalation = func(configuration discovery.Configuration,
		scaffold characterise.SuitePlan,
	) characterise.SuitePlan {
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
			scaffold characterise.SuitePlan,
		) characterise.SuitePlan {
			return scaffold
		}
	})
}

// SetSharedFileOrderSeed stages every scenario for one module in one shared
// file. Its caller is sequential for the hook's complete lifetime.
func SetSharedFileOrderSeed(t *testing.T, moduleDir, order string) {
	t.Helper()
	seedSharedFileOrder = func(configuration discovery.Configuration) string {
		if configuration.ModuleDir != moduleDir {
			return ""
		}

		return order
	}
	t.Cleanup(func() {
		seedSharedFileOrder = func(discovery.Configuration) string { return "" }
	})
}

// SetCharacteriseWriteSeeds exposes only the five characterisation-write
// controls to the external test package. Its callers are deliberately
// sequential: the hooks are package globals, so their complete lifetime must
// not overlap any other engine-seam test.
//
//nolint:revive,nolintlint // integration build excludes the revive finding.
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

// SetGitTimeout shortens the fixed cap on git invocations for one test: the
// deadline test drives a sleeping git through the real seam, and no caller
// parameter changes the cap. Its callers are sequential for the hook's
// complete lifetime.
func SetGitTimeout(t *testing.T, limit time.Duration) {
	t.Helper()

	previous := gitTimeout
	gitTimeout = limit

	t.Cleanup(func() { gitTimeout = previous })
}
