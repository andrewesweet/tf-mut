package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The M4.5 closure rule, enforced the way M2's, M3's and M4's are: `just
// gate-m45` has to name its cases and every name has to resolve to a test that
// exists, so the gate can never go green by naming nothing.

const m45GateRecipe = "gate-m45:"

// minimumM45GateCases guards against a recipe edit that empties the gate.
const minimumM45GateCases = 45

func TestTheM45GateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := m45GatedTests(t)
	if len(named) < minimumM45GateCases {
		t.Fatalf("the M4.5 gate names %d cases, which is fewer than the milestone requires",
			len(named))
	}

	declared := m4TestDeclarations(t)

	for _, name := range named {
		if !declared[name] {
			t.Fatalf("the M4.5 gate names %s, which no test declares", name)
		}
	}
}

func TestTheM45GateCoversEveryNamedRequirement(t *testing.T) {
	t.Parallel()

	// The behaviours issue #71's exit gates require, each mapped to the test
	// that proves it. A gate that stopped running one of these would still be
	// green, which is precisely the failure this catches.
	required := map[string]string{
		"#70: HCL check-scoped provider":           "TestACheckScopedDataSourceReachesTheProviderInventory",
		"#70: HCL check-scoped effect":             "TestACheckScopedDataSourceReachesTheEffectInventory",
		"#70: HCL removed provider":                "TestARemovedBlockReachesTheProviderInventory",
		"#70: HCL removed provisioner":             "TestARemovedBlockProvisionerReachesTheEffectInventoryAndNeverExecutes",
		"#70: JSON check-scoped effect":            "TestACheckScopedDataSourceInJSONReachesTheEffectInventory",
		"#70: JSON check-scoped provider":          "TestACheckScopedDataSourceInJSONReachesTheProviderInventory",
		"#70: JSON removed effect":                 "TestARemovedBlockInJSONReachesTheEffectInventory",
		"#70: JSON removed provider":               "TestARemovedBlockInJSONReachesTheProviderInventory",
		"moved is refused by name":                 "TestAMovedBlockIsRefusedInHCL",
		"import is refused by name":                "TestAnImportBlockIsRefusedInHCL",
		"the aliased-provider acceptance pair, a":  "TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn",
		"the aliased-provider acceptance pair, b":  "TestAMissingAliasMockRefusesBeforeExecution",
		"the default characterisation writes not":  "TestTheDefaultCharacterisationWritesNothing",
		"a written suite is green and registered":  "TestAWrittenSuiteIsGreenAndRegistered",
		"the collision rule":                       "TestASecondWriteIsRefusedAsACollision",
		"--force honours the provenance registry":  "TestForceReplacesOnlyUnmodifiedGeneratedFiles",
		"scenarios carry distinct state keys":      "TestScenariosCarryDistinctStateKeys",
		"reorder invariance":                       "TestScenarioPinsAreInvariantUnderFileOrder",
		"the zero-output escalation":               "TestAZeroOutputModuleEscalatesAndSaysSo",
		"a rung that pinned nothing is not done":   "TestARungThatPinsNothingIsNeverComplete",
		"the configured rung's skip classes":       "TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined",
		"sensitivity at the pin point":             "TestASensitiveValueReachesNoGeneratedArtefact",
		"a secret in a failed attempt":             "TestASecretInAFailedAttemptReachesNoArtefact",
		"real reports validate against 2.3.0":      "TestARealCharacterisationReportValidatesAgainstThePublishedSchema",
		"branch expansion":                         "TestBranchExpansionPinsBothSidesOfAConditional",
		"the non-executable artefact class":        "TestAnUnsynthesizableInputBecomesANonExecutableArtefact",
		"answer, resume, promote, green":           "TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen",
		"todos carries the whole evidence bundle":  "TestTodosListsTheOpenJudgementPointsWithTheirEvidence",
		"until-dry converges with no write":        "TestUntilDryConvergesWithoutWritingAByte",
		"until-dry respects the ladder":            "TestUntilDryRespectsTheGranularityLadder",
		"curate refuses a partial population":      "TestCurateRefusesAPartialPopulationAtConfigurationTime",
		"curate reports with its evidence":         "TestCurateReportsAnEmptyKillSetWithItsEvidence",
		"curate is report-only":                    "TestCurateWritesNothing",
		"the multi-assertion kill-set fact":        "TestOneMutantFailingTwoAssertionsAttributesBoth",
		"unassertable constructs scaffold":         "TestUnassertableConstructsBecomeNonExecutableScaffolds",
		"characterise reachable through the CLI":   "TestCharacteriseIsWiredThroughTheCommandLine",
		"the todo surfaces are wired":              "TestTheTodoSurfacesAreWiredInBothArgumentOrders",
		"trailing arguments refused: characterise": "TestCharacteriseRefusesArgumentsAfterTheModulePath",
		"trailing arguments refused: todos":        "TestTodosRefusesArgumentsAfterTheModulePath",
		"the end-of-MVP walkthrough executes":      "TestTheInstalledSkillsWalkthroughExecutes",
		"the walkthrough is falsifiable":           "TestASeededWrongFlagInTheSkillTurnsTheGateRed",
		"a refuted answer is rejected, not fatal":  "TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure",
		"the input-closure race at the probe":      "TestAClosureChangeAtTheProbeYieldsZeroWrites",
		"a staged refusal costs no Terraform run":  "TestNoTerraformRunPrecedesAStagedGateRefusal",
		"curate reachable through the CLI":         "TestCurateIsWiredThroughTheCommandLine",
		"trailing arguments refused: curate":       "TestCurateRefusesArgumentsAfterTheModulePath",
		"the mined rung fires when it is reached":  "TestAMinedValidationResolvesAnInputWithNoDefault",
	}

	assertGateCovers(t, "M4.5", m45GatedTests(t), required)
}

func m45GatedTests(t *testing.T) []string {
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

	start := strings.Index(recipe, m45GateRecipe)
	if start < 0 {
		t.Fatalf("%s declares no gate-m45 recipe", justfilePath)
	}

	recipe = recipe[start:]
	if end := strings.Index(recipe, "\n\n"); end > 0 {
		recipe = recipe[:end]
	}

	return gateName().FindAllString(recipe, -1)
}
