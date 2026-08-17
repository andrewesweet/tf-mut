package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M3 closure rule (#54), enforced the way M2's was: the gate has to name
// its cases and every name has to resolve to a test that exists — `just
// gate-m3` must never go green by naming nothing.

const m3GateRecipe = "gate-m3:"

// minimumM3GateCases guards against a recipe edit that empties the gate.
const minimumM3GateCases = 40

func TestTheM3GateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := m3GatedTests(t)
	if len(named) < minimumM3GateCases {
		t.Fatalf("the M3 gate names %d cases, which is fewer than the milestone requires",
			len(named))
	}

	declared := testDeclarations(t)

	for _, name := range named {
		if !declared[name] {
			t.Fatalf("the M3 gate names %s, which no test declares", name)
		}
	}
}

func TestTheM3GateCoversEveryNamedRequirement(t *testing.T) {
	t.Parallel()

	// The behaviours issue #33's offline gates require, each mapped to the
	// test that proves it. A gate that stopped running one of these would
	// still be green, which is precisely the failure this catches.
	required := map[string]string{
		"the C2 soundness pair, permitting half":  "TestAnOutOfConeUnknownPermitsPlanModeUnobservable",
		"the C2 soundness pair, same-resource":    "TestOwnResourceUnknownsStayInCone",
		"the C2 structural guard":                 "TestTheContractFixtureClassifiesIdenticallyUnderTheShortcut",
		"static pre-classification equality":      "TestStaticUnobservableEqualsTheExecutedVerdict",
		"C1 case one, body-only stays NoCoverage": "TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage",
		"C1 case two, in-condition executes":      "TestAMutantInsideTheConditionExecutes",
		"C1 case three, upstream executes":        "TestAMutantUpstreamOfTheConditionExecutes",
		"fail-closed evaluator categories":        "TestExcludedCategoriesFailClosedToExecution",
		"the adapter sweep over generation sites": "TestEveryGenerationSiteMapsIntoTheGraph",
		"the seeded missing-edge check":           "TestASeededMissingEdgeFailsTheSupplementalCheck",
		"the working-tree union":                  "TestAnUncommittedNewResourceIsSelected",
		"unresolvable refs error":                 "TestAMissingRefIsAnError",
		"non-.tf classes force the full run":      "TestNonTerraformClassChangesForceTheFullPopulation",
		"cache invalidation per key dimension":    "TestCacheInvalidationPerKeyDimension",
		"cache refused under the unsafe opt-ins":  "TestCacheIsRefusedUnderTheUnsafeOptIns",
		"corruption as a miss":                    "TestCorruptionIsAMiss",
		"killed regressed to survived is new":     "TestAdoptionThenRegression",
		"accepted indeterminate turning new":      "TestAnAcceptedIndeterminateTurningActionableIsNew",
		"unobserved distinguished from stale":     "TestStaleAndUnobservedAreDistinguished",
		"baseline writes need the full run":       "TestBaselineWriteIsRefusedOffTheFullPopulation",
		"the sampled gate needs its opt-in":       "TestASampledGateIsRefusedWithoutTheOptIn",
		"verdict invariance under --since":        "TestVerdictInvarianceUnderSince",
		"verdict invariance under the cache":      "TestASecondUnchangedRunIsAllCacheHits",
		"verdict invariance under sampling":       "TestVerdictInvarianceUnderSampling",
		"the cached row of the gate table":        "TestTheCachedRowOfTheGateTable",
		"scoped min-score labelled partial":       "TestScopedMinScoreIsLabelledPartial",
		"the lock key dimension":                  "TestTheLockFileIsAKeyDimension",
		"the remote-payload key dimension":        "TestRemoteModulePayloadsAreAKeyDimension",
		"the auto-var key dimension":              "TestAutoVarFilesAreAKeyDimension",
		"the provider-environment key dimension":  "TestProviderEnvironmentIsAKeyDimension",
		"an existing loose cache dir corrected":   "TestAnExistingLooseCacheDirectoryIsCorrected",
		"auto-loaded variable files fail closed":  "TestAnAutoLoadedVariableFileDefeatsTheDefault",
		"ignored untracked files still select":    "TestAnIgnoredUntrackedVariableFileForcesTheFullPopulation",
		"provider configuration unbounds cones":   "TestProviderConfigurationMakesTheConeUnbounded",
		"module wiring against terraform graph":   "TestModuleWiringAgreesWithTerraformGraph",
	}

	named := map[string]bool{}
	for _, name := range m3GatedTests(t) {
		named[name] = true
	}

	for behaviour, test := range required {
		if !named[test] {
			t.Fatalf("the M3 gate does not run %s, which proves %s", test, behaviour)
		}
	}
}

func m3GatedTests(t *testing.T) []string {
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

	start := strings.Index(recipe, m3GateRecipe)
	if start < 0 {
		t.Fatalf("%s declares no gate-m3 recipe", justfilePath)
	}

	recipe = recipe[start:]
	if end := strings.Index(recipe, "\n\n"); end > 0 {
		recipe = recipe[:end]
	}

	return gateName().FindAllString(recipe, -1)
}
