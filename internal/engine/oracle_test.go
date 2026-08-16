package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The honesty gate. Each case below is a refutation the oracle has to survive:
// a shape of module for which the obvious implementation reports something
// false. They run against the real Terraform binary through the engine seam,
// like every other product test in this package.

func TestUnknownRefinementSurvivesRatherThanBeingExcluded(t *testing.T) {
	t.Parallel()

	// R2-2. `terraform_data.derived.input` is unknown at plan time but cty
	// refines it with a known prefix the plan serialisation discards, so
	// mutating the prefix produces a byte-identical plan and a `startswith`
	// assertion still kills it. Excluding it would be a false proof.
	result := runFixture(t, "unknown-refinement")

	mutants := survivorsAt(result, "local.prefix")
	if len(mutants) == 0 {
		t.Fatalf("no survivor mutated the prefix; sites are %v", sites(result))
	}

	for _, mutant := range mutants {
		if mutant.State == report.Unobservable {
			t.Fatalf("mutant %s was excluded as unobservable over a payload carrying unknowns", mutant.ID)
		}

		if mutant.Verdict.Diagnosis != report.IndeterminateUnknownValues {
			t.Fatalf("mutant %s diagnosed %q, want %q",
				mutant.ID, mutant.Verdict.Diagnosis, report.IndeterminateUnknownValues)
		}

		if len(mutant.Verdict.Evidence.UnknownPaths) == 0 {
			t.Fatalf("mutant %s carries no unknown paths as evidence", mutant.ID)
		}
	}

	if result.Count(report.Unobservable) > 0 {
		t.Fatalf("a plan-mode run excluded %d mutant(s) as unobservable", result.Count(report.Unobservable))
	}
}

func TestAKnownPayloadLetsTheOracleProveUnobservability(t *testing.T) {
	t.Parallel()

	// The other half of the conservative rule: over an apply-mode payload with
	// no unknown in it, a mutant nothing can distinguish is excluded and says so.
	result := runFixture(t, "oracle")

	unread := survivorsAt(result, "local.unread")
	if len(unread) > 0 {
		t.Fatalf("a local nothing reads survived: %+v", unread[0].State)
	}

	excluded := false

	for _, mutant := range result.Mutants {
		if mutant.Site != "local.unread" {
			continue
		}

		if mutant.State != report.Unobservable {
			t.Fatalf("mutant of an unread local is %s, want %s", mutant.State, report.Unobservable)
		}

		excluded = true

		if mutant.Verdict == nil || mutant.Verdict.Fix == "" {
			t.Fatalf("mutant %s was excluded without an explanation", mutant.ID)
		}

		if mutant.Verdict.Diagnosis != "" {
			t.Fatalf("mutant %s is not a survivor yet carries diagnosis %q", mutant.ID, mutant.Verdict.Diagnosis)
		}
	}

	if !excluded {
		t.Fatalf("no mutant of the unread local was generated; sites are %v", sites(result))
	}

	scored := result.Count(report.Killed) + result.Count(report.KilledByError) +
		result.Count(report.Survived) + result.Count(report.StructurallyUnassertable) +
		result.Count(report.NoCoverage) + result.Count(report.Timeout)

	if result.Metrics.Scored != scored || result.Count(report.Unobservable) == 0 {
		t.Fatalf("scored set = %d, want %d with %d unobservable mutant(s) excluded",
			result.Metrics.Scored, scored, result.Count(report.Unobservable))
	}
}

func TestPhaseTwoRunsOnlyForPhaseOneSurvivors(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "skeleton")

	for _, mutant := range result.Mutants {
		verbose := 0

		for _, run := range mutant.Runs {
			if run.Phase == 2 {
				verbose++
			}
		}

		fingerprinted := mutant.State == report.Survived ||
			mutant.State == report.Unobservable ||
			mutant.State == report.StructurallyUnassertable

		if fingerprinted && verbose == 0 {
			t.Fatalf("%s mutant %s was classified without a fingerprint run", mutant.State, mutant.ID)
		}

		if !fingerprinted && verbose > 0 {
			t.Fatalf("%s mutant %s paid for a fingerprint run it did not need", mutant.State, mutant.ID)
		}
	}
}

func TestADeltaSeenOnlyThroughALocalAndAnOutputIsNotNoAssertion(t *testing.T) {
	t.Parallel()

	// The mandatory C3 reproduction. Address intersection alone would report
	// "no assertion reads this"; the forward closure has to find the read.
	result := runFixture(t, "closure")

	graded := survivorsAt(result, "terraform_data.graded.input")
	if len(graded) == 0 {
		t.Fatalf("nothing survived at the graded resource; sites are %v", sites(result))
	}

	for _, mutant := range graded {
		if mutant.Verdict.Diagnosis == report.NoAssertion {
			t.Fatalf("mutant %s diagnosed no-assertion, but an assertion reads it through "+
				"local.summary and output.summary", mutant.ID)
		}

		if mutant.Verdict.Diagnosis != report.WeakAssertion {
			t.Fatalf("mutant %s diagnosed %q, want %q",
				mutant.ID, mutant.Verdict.Diagnosis, report.WeakAssertion)
		}

		if mutant.Verdict.Evidence.Assertion == "" {
			t.Fatalf("mutant %s names no assertion as evidence", mutant.ID)
		}
	}
}

func TestADefeatedClosureDiagnosesUnassertedAndNamesTheConstruct(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "closure")

	found := false

	for _, mutant := range result.Survivors() {
		if mutant.Verdict.Diagnosis != report.Unasserted {
			continue
		}

		found = true

		if mutant.Verdict.Evidence.DefeatedBy == "" {
			t.Fatalf("mutant %s is unasserted without naming what defeated the closure", mutant.ID)
		}

		if !strings.Contains(mutant.Verdict.Evidence.DefeatedBy, "splat") {
			t.Fatalf("mutant %s blames %q, want the splat projection",
				mutant.ID, mutant.Verdict.Evidence.DefeatedBy)
		}
	}

	if !found {
		t.Fatalf("no survivor fell back to the honest unasserted diagnosis: %v", diagnoses(result))
	}
}

func TestAnUnreadResourceStillDiagnosesNoAssertion(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "closure")

	ignored := survivorsAt(result, "terraform_data.ignored.input")
	if len(ignored) == 0 {
		t.Fatalf("nothing survived at the unread resource; sites are %v", sites(result))
	}

	for _, mutant := range ignored {
		if mutant.Verdict.Diagnosis != report.NoAssertion {
			t.Fatalf("mutant %s diagnosed %q, want %q",
				mutant.ID, mutant.Verdict.Diagnosis, report.NoAssertion)
		}

		if len(mutant.Verdict.Evidence.Delta) == 0 {
			t.Fatalf("mutant %s carries no masked delta as evidence", mutant.ID)
		}
	}
}

func TestAVolatileTemplateStillYieldsAFindingOnItsStableSuffix(t *testing.T) {
	t.Parallel()

	// R2-9. Masking the whole value would erase the finding; masking only the
	// impure component keeps it, and repeated runs never flake.
	result := runFixture(t, "volatile")

	found := false

	for _, mutant := range result.Survivors() {
		if !strings.Contains(mutant.Site, "terraform_data.token") {
			continue
		}

		for _, change := range mutant.Verdict.Evidence.Delta {
			if strings.Contains(change.Baseline, "-stable") {
				found = true
			}
		}
	}

	if !found {
		t.Fatalf("no survivor carried the stable suffix in its delta: %v", diagnoses(result))
	}
}

func TestADeterministicIdentifierIsNeverMasked(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "volatile")

	killed := false

	for _, mutant := range result.Mutants {
		if mutant.Site != "terraform_data.derived.input" {
			continue
		}

		if mutant.State == report.Killed {
			killed = true
		}

		if mutant.State == report.Unobservable {
			t.Fatalf("a uuidv5 mutation was masked away: %s", mutant.ID)
		}
	}

	if !killed {
		t.Fatalf("no deterministic identifier mutation was killed; sites are %v", sites(result))
	}
}

func TestVolatilityFixturesClassifyIdenticallyAcrossRepeatedRuns(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"volatile", "mutant-volatile"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			first := verdicts(runFixture(t, fixture))

			for range 2 {
				repeated := verdicts(runFixture(t, fixture))
				if !slices.Equal(first, repeated) {
					t.Fatalf("verdicts moved between runs:\n  %v\n  %v", first, repeated)
				}
			}
		})
	}
}

func TestVolatilityIntroducedOnlyByTheMutantIsDecidedByARerun(t *testing.T) {
	t.Parallel()

	// The C4 reproduction. Both baseline runs take the stable arm, so the
	// two-run diff has nothing to mask; the mutant that takes the other arm
	// moves every run, and masking it against the baseline would claim an
	// equality nobody can stand behind.
	result := runFixture(t, "mutant-volatile")

	found := false

	for _, mutant := range result.Survivors() {
		if mutant.Verdict.Diagnosis != report.IndeterminateVolatility {
			continue
		}

		found = true

		if len(mutant.Verdict.Evidence.UnstableAttributes) == 0 {
			t.Fatalf("mutant %s is indeterminate without naming what moved", mutant.ID)
		}
	}

	if !found {
		t.Fatalf("no mutant-introduced volatility was detected: %v", diagnoses(result))
	}

	for _, mutant := range result.Mutants {
		if mutant.State == report.Unobservable && mutant.Site == "terraform_data.token.input" {
			assertStableArm(t, mutant)
		}
	}
}

// assertStableArm guards the other direction: a mutant that collapses the
// conditional to the arm the baseline already takes really is unobservable, and
// the volatility rule must not sweep it up.
func assertStableArm(t *testing.T, mutant report.Mutant) {
	t.Helper()

	if !strings.Contains(mutant.Diff, "\"fixed\"") {
		t.Fatalf("mutant %s was excluded although it did not select the stable arm:\n%s",
			mutant.ID, mutant.Diff)
	}
}

func TestConstructsWithNoProjectionAreStructurallyUnassertable(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	wanted := map[string]bool{
		"DEPENDS-DROP":          false,
		"VAR-VALIDATION-REMOVE": false,
		"PRE-POST-REMOVE":       false,
	}

	for _, mutant := range result.Mutants {
		if _, tracked := wanted[mutant.Operator]; !tracked {
			continue
		}

		if mutant.State != report.StructurallyUnassertable {
			t.Fatalf("%s mutant %s is %s, want %s",
				mutant.Operator, mutant.ID, mutant.State, report.StructurallyUnassertable)
		}

		if mutant.Verdict == nil || !strings.Contains(mutant.Verdict.Fix, "expect_failures") &&
			!strings.Contains(mutant.Verdict.Fix, "accept") {
			t.Fatalf("%s mutant %s carries no fix guidance", mutant.Operator, mutant.ID)
		}

		wanted[mutant.Operator] = true
	}

	for operator, seen := range wanted {
		if !seen {
			t.Fatalf("no %s mutant was generated; sites are %v", operator, sites(result))
		}
	}

	if result.Count(report.StructurallyUnassertable) == 0 {
		t.Fatal("the state never fired")
	}
}

func TestStructurallyUnassertableSitsInTheDenominator(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	counted := result.Count(report.Killed) + result.Count(report.KilledByError) +
		result.Count(report.Survived) + result.Count(report.StructurallyUnassertable) +
		result.Count(report.NoCoverage) + result.Count(report.Timeout)

	if result.Metrics.Scored != counted {
		t.Fatalf("scored set = %d, want %d: an untested contract is a finding, not noise",
			result.Metrics.Scored, counted)
	}
}

func TestAnExercisedValidationKillsItsMutants(t *testing.T) {
	t.Parallel()

	// The other direction of the contract story: where a run block does exercise
	// the rule with expect_failures, the weakened condition is caught.
	result := runFixture(t, "expect-failures")

	for _, mutant := range result.Mutants {
		if !strings.HasPrefix(mutant.Operator, "VAR-VALIDATION") {
			continue
		}

		if mutant.State != report.Killed && mutant.State != report.KilledByError {
			t.Fatalf("%s mutant %s is %s: an exercised validation must catch it",
				mutant.Operator, mutant.ID, mutant.State)
		}
	}
}

func TestTheHigherDiagnosisWinsWhenTwoPredicatesHold(t *testing.T) {
	t.Parallel()

	t.Run("unknown values outrank volatility", func(t *testing.T) {
		t.Parallel()

		// A plan-mode payload always carries unknowns, and the conditional
		// exposes volatility only under mutation: both predicates hold, and the
		// higher one wins.
		result := runFixture(t, "precedence-unknown")

		found := false

		for _, mutant := range result.Survivors() {
			if mutant.Verdict.Diagnosis == report.IndeterminateVolatility {
				t.Fatalf("mutant %s took the lower diagnosis while unknowns were present", mutant.ID)
			}

			if mutant.Verdict.Diagnosis == report.IndeterminateUnknownValues {
				found = true
			}
		}

		if !found {
			t.Fatalf("the higher diagnosis never fired: %v", diagnoses(result))
		}
	})

	t.Run("weak assertion outranks no assertion", func(t *testing.T) {
		t.Parallel()

		// The delta spans an address an assertion reads and one it does not.
		result := runFixture(t, "closure")

		for _, mutant := range survivorsAt(result, "terraform_data.graded.input") {
			if mutant.Verdict.Diagnosis != report.WeakAssertion {
				t.Fatalf("mutant %s diagnosed %q while an assertion reads part of its delta",
					mutant.ID, mutant.Verdict.Diagnosis)
			}
		}
	})

	t.Run("no assertion outranks unasserted only when the closure holds", func(t *testing.T) {
		t.Parallel()

		// `no-assertion` claims a proof. Where a projection defeats the closure
		// the proof does not exist, so the honest fallback takes over — the two
		// predicates cannot both hold, and this asserts the boundary.
		result := runFixture(t, "closure")

		for _, mutant := range result.Survivors() {
			evidence := mutant.Verdict.Evidence

			if mutant.Verdict.Diagnosis == report.NoAssertion && evidence.DefeatedBy != "" {
				t.Fatalf("mutant %s claims a proof while naming %q as having defeated it",
					mutant.ID, evidence.DefeatedBy)
			}

			if mutant.Verdict.Diagnosis == report.Unasserted && evidence.DefeatedBy == "" {
				t.Fatalf("mutant %s fell back to unasserted without a defeating construct", mutant.ID)
			}
		}
	})
}

func TestStateAndDiagnosisAreIndependentOfOrderAndParallelism(t *testing.T) {
	t.Parallel()

	// R2-8. Overlapping predicates without a precedence would let file order or
	// worker scheduling decide a verdict.
	module := copyFixture(t, "closure")

	sequential := runWithJobs(t, module, 1)
	concurrent := runWithJobs(t, module, oversubscribedJobs)

	if !slices.Equal(verdicts(sequential), verdicts(concurrent)) {
		t.Fatalf("verdicts depend on parallelism:\n  %v\n  %v",
			verdicts(sequential), verdicts(concurrent))
	}

	// The same population, reached through a second module whose files sort the
	// other way round, must classify identically.
	renamed := copyFixture(t, "closure")
	renameFile(t, renamed, "main.tf", "zz-main.tf")

	reordered, err := engine.Run(t.Context(), baseConfig(t, renamed))
	if err != nil {
		t.Fatalf("run after renaming: %v", err)
	}

	if !slices.Equal(statesOnly(sequential), statesOnly(reordered)) {
		t.Fatalf("verdicts depend on file order:\n  %v\n  %v",
			statesOnly(sequential), statesOnly(reordered))
	}
}
