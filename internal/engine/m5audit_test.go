package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M5 closure rule, enforced the way M2's, M3's, M4's and M4.5's are:
// `just gate-m5` has to name its cases and every name has to resolve to a
// test that exists, so the gate can never go green by naming nothing. M5a's
// lifecycle cases and M5c.1's pack cases are both carried here.

const m5GateRecipe = "gate-m5:"

// minimumM5GateCases guards against a recipe edit that empties the gate.
const minimumM5GateCases = 30

func TestTheM5GateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := m5GatedTests(t)
	if len(named) < minimumM5GateCases {
		t.Fatalf("the M5 gate names %d cases, which is fewer than the milestone requires",
			len(named))
	}

	// The by-name flag refusal lives beside the flag table in cmd, so the
	// declarations walk covers both trees, as the M4 audit's does.
	declared := m4TestDeclarations(t)

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
		"LC-IGNORE-DROP witness kills":       "TestTheIgnoreDropWitnessKillsThroughTheSeam",
		"LC-IGNORE-ALL witness kills":        "TestTheIgnoreAllWitnessKillsThroughTheSeam",
		"LC-REPLACE-TRIGGER-DROP kills":      "TestTheReplaceTriggerWitnessKillsThroughTheSeam",
		"every operator has an offline site": "TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture",
		"identical fingerprint unassertable": "TestALifecycleMutantWithAnIdenticalFingerprintIsStructurallyUnassertable",
		"module NoCoverage outranks Tier 4":  "TestModuleLevelNoCoverageKeepsItsPrecedenceOverALifecycleMutant",
		"conditional NoCoverage outranks LC": "TestConditionalNoCoverageKeepsItsPrecedenceOverALifecycleMutant",
		"deep includes standard, not Tier 4": "TestDeepIncludesStandardAndStandardExcludesTheLifecycleOperators",
		"pseudo-tested count over extreme":   "TestThePseudoTestedCountStaysOverTheExtremeTier",
		"standard report is invariant":       "TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators",
		"matrix sweep holds Tier 4 sites":    "TestEveryEnabledOperatorHasAGenerationSite",
		"catalogue rows and matrix agree":    "TestEveryEnabledOperatorHasAMatrixRow",
		"matrix rows name enabled ops":       "TestEveryMatrixRowNamesAnEnabledOperator",
		"Tier 4 mutants parse":               "TestTheMatrixFixtureGeneratesOnlyParseableMutants",

		// M5c.1: the pack mechanism and the user-defined pack surface.
		"pack generates, classifies, suggests": "TestAUserPackGeneratesClassifiesAndSuggestsThroughTheSeam",
		"origins on a collapsed boolean flip":  "TestOriginsNameThePackEntryOnACollapsedBooleanFlip",
		"red proof: aggregation disabled":      "TestDisablingOriginAggregationTurnsTheOriginsCaseRed",
		"reversed ownership loses nothing":     "TestReversingOwnershipLosesNoContributor",
		"every contract row refused by name":   "TestEveryPackContractRowIsRefusedByName",
		"unsupported form is a summary no-op":  "TestAnUnsupportedAttributeFormIsANoOpInThePackSummary",
		"schema evidence refuses undescribed":  "TestSchemaEvidenceRefusesAnUndescribedAttribute",
		"type-incompatible to finds no site":   "TestATypeIncompatibleReplacementFindsNoSite",
		"flag and configuration union":         "TestFlagAndConfiguredPacksMergeAsAUnion",
		"configured narrowing refused":         "TestAConfiguredPackSelectionIsRefusedOnCurateAndUntilDry",
		"language operator owns shared row":    "TestALanguageOperatorOwnsARowAPackEntryAlsoProduces",
		"--pack refused by name":               "TestThePackFlagIsRefusedByNameOnCharacteriseTodosAndCurate",
		"--pack wired, unknown exits 2":        "TestPacksAreWiredThroughTheCommandLine",
		"edited pack is a cache miss":          "TestAnEditedUserPackIsACacheMiss",
		"stale verified suggestion refused":    "TestAStaleVerifiedPackSuggestionIsRefused",
		"changed pack forces full population":  "TestAChangedPackFileForcesTheFullPopulationUnderSince",
		"out-of-closure pack forces full":      "TestAChangedPackOutsideTheClosureForcesTheFullPopulationUnderSince",
		"no pack in standard":                  "TestNoPackEntersTheStandardPopulation",
		"pack operators have offline sites":    "TestEveryPackOperatorHasASiteInTheOfflineFixture",
		"pack reports validate against 2.4.0":  "TestARealPackReportValidatesAgainstThePublishedSchema",

		// M5-0.5a: the opportunity census's classification vocabulary,
		// denominator rules and counts.
		"undecidable constraint classifies":  "TestTheOpportunityCensusClassifiesUndecidableConstraints",
		"refused typed candidate classifies": "TestTheOpportunityCensusClassifiesRefusedTypedCandidates",
		"missing typed candidate classifies": "TestTheOpportunityCensusClassifiesMissingTypedCandidates",
		"sensitive evidence withheld":        "TestTheOpportunityCensusWithholdsRedactedEvidence",
		"JSON variables in the denominator":  "TestTheCensusDenominatorCountsJSONDeclaredVariables",
		"empty JSON stratum unmeasured":      "TestAnEmptyJSONStratumIsPublishedAsUnmeasured",
		"mined count splits by stratum":      "TestTheMinedCountsSplitByStratum",
		"census reading is consistent":       "TestTheCensusReadingIsInternallyConsistent",
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
