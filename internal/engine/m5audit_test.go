package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M5a closure rule, enforced the way M2's, M3's, M4's and M4.5's are:
// `just gate-m5` has to name its cases and every name has to resolve to a
// test that exists, so the gate can never go green by naming nothing.

const m5GateRecipe = "gate-m5:"

// minimumM5GateCases guards against a recipe edit that empties the gate.
const minimumM5GateCases = 12

func TestTheM5GateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := m5GatedTests(t)
	if len(named) < minimumM5GateCases {
		t.Fatalf("the M5 gate names %d cases, which is fewer than the milestone requires",
			len(named))
	}

	declared := testDeclarations(t)

	for _, name := range named {
		if !declared[name] {
			t.Fatalf("the M5 gate names %s, which no test declares", name)
		}
	}
}

func TestTheM5GateCoversEveryNamedRequirement(t *testing.T) {
	t.Parallel()

	// The behaviours the M5a slice requires, each mapped to the test that
	// proves it. A gate that stopped running one of these would still be
	// green, which is precisely the failure this catches.
	required := map[string]string{
		"the LC-IGNORE-DROP witness kills through the seam":    "TestTheIgnoreDropWitnessKillsThroughTheSeam",
		"the LC-IGNORE-ALL witness kills through the seam":     "TestTheIgnoreAllWitnessKillsThroughTheSeam",
		"the LC-REPLACE-TRIGGER-DROP witness kills":            "TestTheReplaceTriggerWitnessKillsThroughTheSeam",
		"every admitted operator has an offline site":          "TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture",
		"identical fingerprints are StructurallyUnassertable":  "TestALifecycleMutantWithAnIdenticalFingerprintIsStructurallyUnassertable",
		"module-level NoCoverage outranks the lifecycle shape": "TestModuleLevelNoCoverageKeepsItsPrecedenceOverALifecycleMutant",
		"conditional NoCoverage outranks the lifecycle shape":  "TestConditionalNoCoverageKeepsItsPrecedenceOverALifecycleMutant",
		"deep includes standard, standard excludes Tier 4":     "TestDeepIncludesStandardAndStandardExcludesTheLifecycleOperators",
		"the pseudo-tested count stays over the extreme tier":  "TestThePseudoTestedCountStaysOverTheExtremeTier",
		"the matrix fixture's standard report is invariant":    "TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators",
		"the matrix sweep holds the Tier 4 sites":              "TestEveryEnabledOperatorHasAGenerationSite",
		"the catalogue rows and the matrix agree":              "TestEveryEnabledOperatorHasAMatrixRow",
		"the matrix rows name enabled operators":               "TestEveryMatrixRowNamesAnEnabledOperator",
		"the Tier 4 mutants parse":                             "TestTheMatrixFixtureGeneratesOnlyParseableMutants",
	}

	assertGateCovers(t, "M5", m5GatedTests(t), required)
}

func m5GatedTests(t *testing.T) []string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	content, err := os.ReadFile(filepath.Join(root, justfilePath)) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", justfilePath, err)
	}

	recipe := string(content)

	start := strings.Index(recipe, m5GateRecipe)
	if start < 0 {
		t.Fatalf("%s declares no gate-m5 recipe", justfilePath)
	}

	recipe = recipe[start:]
	if end := strings.Index(recipe, "\n\n"); end > 0 {
		recipe = recipe[:end]
	}

	return gateName().FindAllString(recipe, -1)
}
