package engine_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5a Tier 4 slice. The lifecycle fixture carries every admitted
// operator's site together with the day-two run-block sequence the M5-0.1
// measurement killed it under, so each witness here is a re-execution of
// recorded evidence rather than a restatement of it: original baseline-green,
// mutant Killed through the seam, against the suite on disk.

// admittedLifecycleOperators is the Tier 4 set M5-0.1 admitted on its
// kill witnesses.
//
//nolint:gochecknoglobals // the operator set is the milestone's own table fixture.
var admittedLifecycleOperators = []string{
	string(mutation.LCIgnoreDrop),
	string(mutation.LCIgnoreAll),
	string(mutation.LCReplaceTriggerDrop),
}

// lifecycleWitnesses maps each admitted operator to the site its witness
// killed and the fixture assertion the kill turned on. The assertion text is
// checked against the fixture on disk, so editing the killing assertion away
// turns these cases red.
//
//nolint:gochecknoglobals // the witness table is the milestone's own recorded evidence.
var lifecycleWitnesses = map[string]struct {
	site      string
	assertion string
	// killComparison is the assertion's comparison in the form the catalogue
	// fix text names it, subject-generic rather than fixture-specific.
	killComparison string
}{
	string(mutation.LCIgnoreDrop): {
		site:           "terraform_data.held.lifecycle.ignore_changes",
		assertion:      `terraform_data.held.input == "old"`,
		killComparison: `input == "old"`,
	},
	string(mutation.LCIgnoreAll): {
		site:           "terraform_data.wide.lifecycle.ignore_changes",
		assertion:      `terraform_data.wide.input == "new"`,
		killComparison: `input == "new"`,
	},
	string(mutation.LCReplaceTriggerDrop): {
		site:           "terraform_data.replaced.lifecycle.replace_triggered_by",
		assertion:      "terraform_data.replaced.id != run.replace_first.replaced_id",
		killComparison: `id != run.first.subject_id`,
	},
}

// lifecycleDeep runs the engine over the lifecycle fixture restricted to the
// given operators at the deep tier, the tier the admitted operators belong to.
func lifecycleDeep(t *testing.T, operators ...string) report.Report {
	t.Helper()

	return lifecycleDeepModule(t, copyFixture(t, "lifecycle"), operators...)
}

// lifecycleDeepModule is the same run over a caller-supplied module directory,
// for the precedence cases that rewrite a copied fixture first.
func lifecycleDeepModule(t *testing.T, moduleDir string, operators ...string) report.Report {
	t.Helper()

	config := baseConfig(t, moduleDir)
	config.Tier = mutation.TierDeep
	config.IncludeOperators = operators
	config.NoCache = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("lifecycle deep run: %v", err)
	}

	return result
}

// stateAt returns the state of the operator's mutant at the site, which
// stateOf cannot distinguish: two operators share one site string at
// an ignore_changes argument.
func stateAt(t *testing.T, result report.Report, operator, site string) report.Mutant {
	t.Helper()

	for _, mutant := range mutantsWithOperator(result, operator) {
		if mutant.Site == site {
			return mutant
		}
	}

	t.Fatalf("no %s mutant at site %s; sites are %s",
		operator, site, strings.Join(sites(result), ", "))

	return report.Mutant{}
}

// witnessedAssertion reads the fixture's killing assertion from the module
// the run graded, so editing the assertion away turns the witness red.
func witnessedAssertion(t *testing.T, moduleDir, operator string) {
	t.Helper()

	witness := lifecycleWitnesses[operator]
	if !strings.Contains(readFile(t, filepath.Join(moduleDir, "tests", "unit.tftest.hcl")), witness.assertion) {
		t.Fatalf("the lifecycle fixture no longer carries %s's witnessed assertion %q",
			operator, witness.assertion)
	}
}

// TestTheIgnoreDropWitnessKillsThroughTheSeam re-executes the LC-IGNORE-DROP
// witness: the day-two apply/plan pair shares one state_key, the second run
// passes a changed value, and the recorded assertion kills the mutant.
func TestTheIgnoreDropWitnessKillsThroughTheSeam(t *testing.T) {
	t.Parallel()

	result := lifecycleDeep(t, string(mutation.LCIgnoreDrop))
	witnessedAssertion(t, result.Module, string(mutation.LCIgnoreDrop))

	witness := lifecycleWitnesses[string(mutation.LCIgnoreDrop)]
	mutant := stateAt(t, result, string(mutation.LCIgnoreDrop), witness.site)

	if mutant.State != report.Killed {
		t.Fatalf("the ignore-drop witness recorded %s, want %s", mutant.State, report.Killed)
	}

	if mutant.Tier != string(mutation.TierDeep) {
		t.Fatalf("a Tier 4 mutant recorded tier %q, want deep", mutant.Tier)
	}
}

// TestTheIgnoreAllWitnessKillsThroughTheSeam re-executes the LC-IGNORE-ALL
// witness: the day-two pair asserts the attribute the over-broad ignore must
// hold still moves.
func TestTheIgnoreAllWitnessKillsThroughTheSeam(t *testing.T) {
	t.Parallel()

	result := lifecycleDeep(t, string(mutation.LCIgnoreAll))
	witnessedAssertion(t, result.Module, string(mutation.LCIgnoreAll))

	witness := lifecycleWitnesses[string(mutation.LCIgnoreAll)]
	mutant := stateAt(t, result, string(mutation.LCIgnoreAll), witness.site)

	if mutant.State != report.Killed {
		t.Fatalf("the ignore-all witness recorded %s, want %s", mutant.State, report.Killed)
	}

	if mutant.Tier != string(mutation.TierDeep) {
		t.Fatalf("a Tier 4 mutant recorded tier %q, want deep", mutant.Tier)
	}
}

// TestTheReplaceTriggerWitnessKillsThroughTheSeam re-executes the
// LC-REPLACE-TRIGGER-DROP witness: a second apply over the same state_key
// changes the trigger and the id comparison catches the un-replaced subject.
func TestTheReplaceTriggerWitnessKillsThroughTheSeam(t *testing.T) {
	t.Parallel()

	result := lifecycleDeep(t, string(mutation.LCReplaceTriggerDrop))
	witnessedAssertion(t, result.Module, string(mutation.LCReplaceTriggerDrop))

	witness := lifecycleWitnesses[string(mutation.LCReplaceTriggerDrop)]
	mutant := stateAt(t, result, string(mutation.LCReplaceTriggerDrop), witness.site)

	if mutant.State != report.Killed {
		t.Fatalf("the replace-trigger witness recorded %s, want %s", mutant.State, report.Killed)
	}

	if mutant.Tier != string(mutation.TierDeep) {
		t.Fatalf("a Tier 4 mutant recorded tier %q, want deep", mutant.Tier)
	}
}

// TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture is the Tier 4
// site witness: it previews without the provider mirror, and fails if any
// admitted operator has no site. A Tier 4 site witnessed only by a skipped
// test is not witnessed.
func TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture(t *testing.T) {
	t.Parallel()

	result := preview(t, copyFixture(t, "lifecycle"), admittedLifecycleOperators)

	fired := map[string]int{}
	for _, mutant := range result.Mutants {
		fired[mutant.Operator]++
	}

	for _, operator := range admittedLifecycleOperators {
		if fired[operator] == 0 {
			t.Fatalf("admitted operator %s has no generation site in the offline lifecycle fixture", operator)
		}
	}
}

// TestALifecycleMutantWithAnIdenticalFingerprintIsStructurallyUnassertable
// pins the projection class: the non-projecting marking sends an identical
// fingerprint to StructurallyUnassertable — never Unobservable — the verdict
// lands in the scored denominator, and its fix names the witnessed shape and
// assertion. The Projects assertions are the red proof: marking an admitted
// operator projecting turns this case red.
func TestALifecycleMutantWithAnIdenticalFingerprintIsStructurallyUnassertable(t *testing.T) {
	t.Parallel()

	for _, operator := range admittedLifecycleOperators {
		if mutation.Projects(mutation.Operator(operator)) {
			t.Fatalf("%s is marked projecting; the admitted lifecycle operators are non-projecting", operator)
		}
	}

	result := lifecycleDeep(t, admittedLifecycleOperators...)
	unassertable := classifyLifecycleMutants(t, result)

	if len(unassertable) != 2 {
		t.Fatalf("want the two off-witness lifecycle mutants StructurallyUnassertable, got %d",
			len(unassertable))
	}

	// In the denominator: the scored set holds every lifecycle outcome, and
	// the state's count feeds the population arithmetic.
	if got := result.Metrics.Counts[report.StructurallyUnassertable]; got != 2 {
		t.Fatalf("metrics count %d StructurallyUnassertable, want 2", got)
	}

	if result.Metrics.Scored != len(result.Mutants) {
		t.Fatalf("scored set holds %d of %d mutants; nothing in the lifecycle population may leave it",
			result.Metrics.Scored, len(result.Mutants))
	}

	assertTheLifecycleFixesNameTheirWitnesses(t, unassertable)
}

// classifyLifecycleMutants walks the lifecycle population and holds each
// mutant to its class: the witnessed site kills, every other shape is
// StructurallyUnassertable on the identical fingerprint, and nothing is ever
// Unobservable. It returns the unassertable mutants for the fix assertions.
func classifyLifecycleMutants(t *testing.T, result report.Report) []report.Mutant {
	t.Helper()

	var unassertable []report.Mutant

	for _, operator := range admittedLifecycleOperators {
		witness := lifecycleWitnesses[operator]

		for _, mutant := range mutantsWithOperator(result, operator) {
			switch {
			case mutant.Site == witness.site:
				if mutant.State != report.Killed {
					t.Fatalf("%s at its witnessed site recorded %s, want %s",
						operator, mutant.State, report.Killed)
				}
			case mutant.State != report.StructurallyUnassertable:
				t.Fatalf("%s at %s recorded %s, want %s on the identical-fingerprint shape",
					operator, mutant.Site, mutant.State, report.StructurallyUnassertable)
			default:
				unassertable = append(unassertable, mutant)
			}

			if mutant.State == report.Unobservable {
				t.Fatalf("%s at %s is Unobservable; an admitted lifecycle operator is never Unobservable",
					operator, mutant.Site)
			}
		}
	}

	return unassertable
}

// assertTheLifecycleFixesNameTheirWitnesses holds each fix to the day-two
// shape and the killing assertion, and to the apply-mode pointer that names
// the safety gates without implying this tool authorises the apply.
func assertTheLifecycleFixesNameTheirWitnesses(t *testing.T, unassertable []report.Mutant) {
	t.Helper()

	for _, mutant := range unassertable {
		fix := mutant.Verdict.Fix

		for _, required := range []string{"state_key", "apply-mode run", "safety gates"} {
			if !strings.Contains(fix, required) {
				t.Fatalf("the %s fix does not name the witnessed shape: %q", mutant.Operator, fix)
			}
		}

		if !strings.Contains(fix, lifecycleWitnesses[mutant.Operator].killComparison) {
			t.Fatalf("the %s fix does not name the witnessed assertion: %q", mutant.Operator, fix)
		}
	}
}

// TestModuleLevelNoCoverageKeepsItsPrecedenceOverALifecycleMutant drops a
// lifecycle block into the nocoverage fixture's unexercised root module: the
// module-level verdict outranks the identical-fingerprint shape, and the
// mutant is counted without an execution.
func TestModuleLevelNoCoverageKeepsItsPrecedenceOverALifecycleMutant(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "nocoverage")

	writeFile(t, filepath.Join(module, "main.tf"),
		strings.Replace(readFile(t, filepath.Join(module, "main.tf")),
			`resource "terraform_data" "never_planned" {
  input = "root"
}`,
			`resource "terraform_data" "never_planned" {
  input = "root"

  lifecycle {
    ignore_changes = [input]
  }
}`, 1))

	result := lifecycleDeepModule(t, module, string(mutation.LCIgnoreDrop))

	if got := len(result.Mutants); got != 1 {
		t.Fatalf("the unexercised root carries one lifecycle mutant, got %d", got)
	}

	mutant := result.Mutants[0]
	if mutant.State != report.NoCoverage {
		t.Fatalf("a module-level lifecycle mutant is %s, want %s", mutant.State, report.NoCoverage)
	}

	if len(mutant.Runs) != 0 {
		t.Fatalf("the NoCoverage mutant executed %d runs", len(mutant.Runs))
	}

	if got := result.Metrics.Counts[report.NoCoverage]; got != 1 {
		t.Fatalf("metrics count %d NoCoverage, want 1", got)
	}
}

// TestConditionalNoCoverageKeepsItsPrecedenceOverALifecycleMutant drops a
// lifecycle block into the C1 fixture's statically-zero resource: the mutant
// sits behind a multiplicity expression pinned to zero under every relevant
// run, so the conditional verdict outranks the identical-fingerprint shape.
func TestConditionalNoCoverageKeepsItsPrecedenceOverALifecycleMutant(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "conditional-nocoverage")

	writeFile(t, filepath.Join(module, "main.tf"),
		strings.Replace(readFile(t, filepath.Join(module, "main.tf")),
			`resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}`,
			`resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"

  lifecycle {
    ignore_changes = [input]
  }
}`, 1))

	result := lifecycleDeepModule(t, module, string(mutation.LCIgnoreDrop))

	if got := len(result.Mutants); got != 1 {
		t.Fatalf("the gated resource carries one lifecycle mutant, got %d", got)
	}

	mutant := result.Mutants[0]
	if mutant.State != report.NoCoverage {
		t.Fatalf("a lifecycle mutant behind a statically-zero count is %s, want %s",
			mutant.State, report.NoCoverage)
	}

	if len(mutant.Runs) != 0 {
		t.Fatalf("the NoCoverage mutant executed %d runs", len(mutant.Runs))
	}
}

// TestDeepIncludesStandardAndStandardExcludesTheLifecycleOperators proves the
// tier arithmetic on one fixture: standard generates no lifecycle mutant, deep
// generates exactly standard's population plus the admitted operators, and
// each lifecycle mutant records the deep tier.
func TestDeepIncludesStandardAndStandardExcludesTheLifecycleOperators(t *testing.T) {
	t.Parallel()

	standard := preview(t, copyFixture(t, "lifecycle"), nil)
	if len(standard.Mutants) == 0 {
		t.Fatal("the standard population is empty")
	}

	deepConfig := previewRequest(t, copyFixture(t, "lifecycle"))
	deepConfig.Tier = mutation.TierDeep

	deep, err := engine.Run(t.Context(), &deepConfig)
	if err != nil {
		t.Fatalf("deep preview: %v", err)
	}

	standardIDs := make([]string, 0, len(standard.Mutants))
	for _, mutant := range standard.Mutants {
		if slices.Contains(admittedLifecycleOperators, mutant.Operator) {
			t.Fatalf("%s generated under the standard tier", mutant.Operator)
		}

		standardIDs = append(standardIDs, mutant.ID)
	}

	lifecycleIDs := make([]string, 0, 3)

	for _, mutant := range deep.Mutants {
		if slices.Contains(admittedLifecycleOperators, mutant.Operator) {
			lifecycleIDs = append(lifecycleIDs, mutant.ID)

			if mutant.Tier != string(mutation.TierDeep) {
				t.Fatalf("%s recorded tier %q, want deep", mutant.Operator, mutant.Tier)
			}

			continue
		}

		if !slices.Contains(standardIDs, mutant.ID) {
			t.Fatalf("deep carries %s at %s, which standard does not generate", mutant.Operator, mutant.Site)
		}
	}

	if len(deep.Mutants) != len(standardIDs)+len(lifecycleIDs) {
		t.Fatalf("deep generated %d mutants = %d standard + %d lifecycle; the sets overlap or lose rows",
			len(deep.Mutants), len(standardIDs), len(lifecycleIDs))
	}

	if len(lifecycleIDs) == 0 {
		t.Fatal("the deep tier generated no lifecycle mutant")
	}
}

// TestThePseudoTestedCountStaysOverTheExtremeTier runs the smoke and deep
// tiers over the lifecycle fixture: the pseudo-tested findings are identical,
// because the headline stays computed over the extreme operators and the Tier
// 4 additions do not move it.
func TestThePseudoTestedCountStaysOverTheExtremeTier(t *testing.T) {
	t.Parallel()

	smokeConfig := baseConfig(t, copyFixture(t, "lifecycle"))
	smokeConfig.Tier = mutation.TierSmoke
	smokeConfig.NoCache = true

	smoke, err := engine.Run(t.Context(), smokeConfig)
	if err != nil {
		t.Fatalf("smoke run: %v", err)
	}

	// The deep leg is the full deep population, the population a user of the
	// tier actually gets.
	deepConfig := baseConfig(t, copyFixture(t, "lifecycle"))
	deepConfig.Tier = mutation.TierDeep
	deepConfig.NoCache = true

	deep, err := engine.Run(t.Context(), deepConfig)
	if err != nil {
		t.Fatalf("deep run: %v", err)
	}

	smokeFindings := findingAddresses(smoke)
	deepFindings := findingAddresses(deep)

	if len(smokeFindings) == 0 {
		t.Fatal("the smoke run found no pseudo-tested resource; the comparison proves nothing")
	}

	if !slices.Equal(smokeFindings, deepFindings) {
		t.Fatalf("the pseudo-tested findings moved from smoke to deep: smoke %v, deep %v",
			smokeFindings, deepFindings)
	}
}

// TestARealDeepReportValidatesAgainstThePublishedSchema validates a real deep
// run against the published file: since M5a the deep population carries Tier 4
// lifecycle mutants, so the emitted operators must be named in the schema's
// enumeration and the whole document must satisfy every rule the file
// encodes — including the schema_version the binary stamps.
func TestARealDeepReportValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	result := lifecycleDeep(t)

	deep := 0
	for _, mutant := range result.Mutants {
		if mutant.Tier == string(mutation.TierDeep) && slices.Contains(admittedLifecycleOperators, mutant.Operator) {
			deep++
		}
	}

	if deep == 0 {
		t.Fatal("the deep run produced no Tier 4 lifecycle mutant; the validation proves nothing")
	}

	builder := strings.Builder{}
	if err := report.WriteJSON(&builder, result); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := any(nil)
	if err := json.Unmarshal([]byte(builder.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	schema := loadPublishedSchema(t)
	if problems := validateAgainst(schema, schema, document, "$"); len(problems) > 0 {
		t.Fatalf("the real deep report does not validate:\n  %s", strings.Join(problems, "\n  "))
	}
}
