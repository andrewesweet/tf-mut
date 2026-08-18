package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M4 closure rule, enforced the way M2's and M3's are: `just gate-m4` has
// to name its cases and every name has to resolve to a test that exists, so
// the gate can never go green by naming nothing.

const m4GateRecipe = "gate-m4:"

// minimumM4GateCases guards against a recipe edit that empties the gate.
const minimumM4GateCases = 50

func TestTheM4GateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := m4GatedTests(t)
	if len(named) < minimumM4GateCases {
		t.Fatalf("the M4 gate names %d cases, which is fewer than the milestone requires",
			len(named))
	}

	declared := m4TestDeclarations(t)

	for _, name := range named {
		if !declared[name] {
			t.Fatalf("the M4 gate names %s, which no test declares", name)
		}
	}
}

func TestTheM4GateCoversEveryNamedRequirement(t *testing.T) {
	t.Parallel()

	// The behaviours issue #58's exit gates require, each mapped to the test
	// that proves it. A gate that stopped running one of these would still be
	// green, which is precisely the failure this catches.
	required := map[string]string{
		"floor: JSON-only unmocked provider":      "TestUnreadJSONFailsTheRealInfrastructureGateClosed",
		"floor: JSON-only provisioner":            "TestUnreadJSONFailsTheUnsandboxedEffectsGateClosed",
		"floor: gates fail closed independently":  "TestEachSafetyGateFailsClosedIndependentlyOnUnreadJSON",
		"floor: malformed JSON never lifts":       "TestMalformedJSONRetainsTheFloorRatherThanLiftingIt",
		"floor: exclusions hide nothing":          "TestConfiguredExclusionsCannotHideUnreadJSON",
		"floor: zero Terraform runs first":        "TestNoTerraformRunPrecedesAFloorRefusal",
		"floor: JSON auto-var drops shortcuts":    "TestAJSONAutoVariableFileKeepsTheStaticShortcutsDown",
		"the #57 provisioner reproduction":        "TestTheSliceFiresTheEffectsGateFromContent",
		"the #57 false-proof reproduction":        "TestTheSliceRepairsTheMixedModuleFalseProof",
		"slice: floor lifts per read file":        "TestAParseFailureRetainsTheFloorWhileItsNeighboursLiftTheirs",
		"slice: JSON is never a mutation site":    "TestNoJSONFileIsEverAMutationSite",
		"adapter matrix: addressing":              "TestTheAddressAdapterMatrix",
		"adapter matrix: rendering":               "TestTheRenderingContractMatrix",
		"adapter matrix: sensitivity":             "TestTheSensitivityPredicateRefusesBeforeAnythingRenders",
		"a limit is never a refutation":           "TestAGeneratorLimitIsNeverARefutation",
		"sensitive values reach no artefact":      "TestASensitiveValueReachesNoSuggestionArtefact",
		"outcome-table presence rules":            "TestEverySkippedStatusCarriesNoPatchAndAReason",
		"stable suggestion identifiers":           "TestSuggestionIdentifiersAreStableAcrossRunsAndUnrelatedEdits",
		"soundness: both legs with evidence":      "TestVerifiedRequiresBothLegsAndCarriesTheirEvidence",
		"soundness: the wrong-value seed":         "TestASeededWrongValueIsRefutedThroughTheBaselineLeg",
		"soundness: the vacuous seed":             "TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg",
		"verification is never cached":            "TestVerificationIsNeverCached",
		"apply: digest mismatch aborts":           "TestAStaleVerifiedDigestAbortsThePreflightNamingBothDigests",
		"apply: symlinked target aborts":          "TestASymlinkedTargetAbortsBeforeAnyWrite",
		"apply: partial state reported":           "TestAMultiFileApplyReportsAPartialFailureExplicitly",
		"apply: mutants die after a clean apply":  "TestACleanApplyWritesAtomicallyAndTheMutantsDie",
		"apply: JSON test files never written":    "TestAJSONTestFileIsNeverWrittenByApply",
		"skill: edits preserved unless forced":    "TestAUserEditSurvivesAReinstallUnlessForced",
		"skill: self-consistent with the binary":  "TestTheInstalledSkillReferencesOnlyCommandsAndFlagsTheBinaryHas",
		"the outcome-table row sweep":             "TestEveryOutcomeTableRowIsReachableThroughTheSeam",
		"the verification cost statement":         "TestAScopedSuggestRunStatesItsVerificationCost",
		"apply: out-of-closure target aborts":     "TestAnOutOfClosureTargetAbortsThePreflight",
		"apply: unparseable target aborts":        "TestAnUnparseableTargetAbortsThePreflight",
		"slice: exclusions still hide nothing":    "TestExclusionsCannotHideJSONDeclaredContentUnderTheSlice",
		"slice: refusal still free":               "TestNoTerraformRunPrecedesAContentDrivenRefusal",
		"JSON module calls join the closure":      "TestAJSONDeclaredModuleCallJoinsTheClosure",
		"unmodelled run arguments keep the floor": "TestAnUnmodelledJSONRunArgumentRetainsTheFloor",
		"unmodelled nested content keeps floor":   "TestAnUnmodelledNestedTerraformConstructRetainsTheFloor",
		"suggest reachable through the CLI":       "TestSuggestIsWiredThroughTheCommandLine",
		"the patch is the applied bytes":          "TestTheReportedPatchIsTheBytesApplyWrites",
		"the commit re-check closes the race":     "TestAnEditBetweenPreflightAndCommitAbortsTheReplacement",
		"mutant-surfaced secrets reach no report": "TestAMutantSurfacedSecretReachesNoReport",
		"a JSON check block keeps the floor":      "TestACheckBlockInJSONRetainsTheFloor",
		"an unread mock body keeps the floor":     "TestAJSONMockProviderBodyBeyondAliasRetainsTheFloor",
		"trailing CLI arguments are refused":      "TestArgumentsAfterTheModulePathAreRefused",
		"shared assertions collapse":              "TestSurvivorsSharingOneAssertionCollapseIntoOneSuggestion",
		"contradictory suggest flags refused":     "TestADryRunRefusesAnApplySelection",
		"real reports validate against 2.2.0":     "TestARealSuggestReportValidatesAgainstThePublishedSchema",
		"mock-data files are a key dimension":     "TestMockDataFilesAreAKeyDimension",
	}

	assertGateCovers(t, "M4", m4GatedTests(t), required)
}

// assertGateCovers fails when a gate's -run pattern stopped naming a test a
// required behaviour depends on. Shared by every milestone audit.
func assertGateCovers(t *testing.T, gate string, gated []string, required map[string]string) {
	t.Helper()

	named := map[string]bool{}
	for _, name := range gated {
		named[name] = true
	}

	for behaviour, test := range required {
		if !named[test] {
			t.Fatalf("the %s gate does not run %s, which proves %s", gate, test, behaviour)
		}
	}
}

func m4GatedTests(t *testing.T) []string {
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

	start := strings.Index(recipe, m4GateRecipe)
	if start < 0 {
		t.Fatalf("%s declares no gate-m4 recipe", justfilePath)
	}

	recipe = recipe[start:]
	if end := strings.Index(recipe, "\n\n"); end > 0 {
		recipe = recipe[:end]
	}

	return gateName().FindAllString(recipe, -1)
}

// m4TestDeclarations walks internal and cmd both: the skill self-consistency
// case lives beside the usage text it checks.
func m4TestDeclarations(t *testing.T) map[string]bool {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	declared := map[string]bool{}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // a repository-owned path.
		if readErr != nil {
			return readErr //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		for _, name := range gateName().FindAllString(string(content), -1) {
			if declaration(name).MatchString(string(content)) {
				declared[name] = true
			}
		}

		return nil
	}

	for _, tree := range []string{"internal", "cmd"} {
		if err := filepath.WalkDir(filepath.Join(root, tree), walk); err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}

	return declared
}
