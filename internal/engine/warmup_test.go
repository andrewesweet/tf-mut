package engine_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The two admissions here are about the workspace tf-mut stages before any
// test runs: init must be told the same test directory the test run will use,
// or the modules that directory's run blocks call are never installed; and a
// root that declares a backend must not block the provider schema, or the run
// dies before the baseline. Both fixtures are offline: terraform_data and a
// variable echo, no provider beyond the builtin.

// executedAny reports whether at least one mutant ran, which is the claim
// both staging admissions make: the staged workspace let the suite execute
// the population.
func executedAny(t *testing.T, result report.Report) bool {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.ExecutedRuns > 0 {
			return true
		}
	}

	return false
}

// TestUnitTestDirectoryModuleCallsAreAdmittedAndScored drives a suite whose
// unit tests live in tests/unit and call a module from a run block, configured
// through the test directory the way a caller of a suite laid out this way
// configures it. Terraform installs only the modules called by the test
// directory init is told about, so init without -test-directory leaves the
// call uninstalled and the baseline red with "Module not installed".
func TestUnitTestDirectoryModuleCallsAreAdmittedAndScored(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unit-directory-modulecall")

	config := baseConfig(t, module)
	config.TestDirectory = "tests/unit"

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("run produced no mutants")
	}

	if !executedAny(t, result) {
		t.Fatal("no mutant executed; the suite did not exercise the population")
	}
}

// TestDeclaredBackendDoesNotBlockTheSchemaOrTheSuite drives a root that
// declares backend "azurerm" with no configured credentials anywhere. Init
// runs with -backend=false, and the next command in that workspace then
// demands backend initialisation; the first is providers schema, so the run
// died before the baseline. The staged workspace must not carry the backend.
func TestDeclaredBackendDoesNotBlockTheSchemaOrTheSuite(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "backend-declared")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("run produced no mutants")
	}

	if !executedAny(t, result) {
		t.Fatal("no mutant executed; the suite did not exercise the population")
	}
}
