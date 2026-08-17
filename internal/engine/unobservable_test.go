package engine_test

import (
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3a.2 (#46): the path-scoped unknown rule and the structurally guarded
// static Unobservable. The rule: an unknown blocks an equality claim iff its
// path lies in the mutation's forward cone, under the fail-closed adapters —
// and the whole-payload rule remains the floor wherever a mapping fails.

// TestAnOutOfConeUnknownPermitsPlanModeUnobservable is the demo case and half
// of the C2 soundness pair: the plan payload carries unknowns (the noise
// resource's id and output), none of them in the cone of a mutation inside
// output.tier, so the oracle proves unobservability in plan mode for the
// first time.
func TestAnOutOfConeUnknownPermitsPlanModeUnobservable(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "out-of-cone")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	found := false

	for _, mutant := range result.Mutants {
		if mutant.Site != "output.tier.value" {
			continue
		}

		if mutant.State == report.Killed {
			continue
		}

		found = true

		if mutant.State != report.Unobservable {
			t.Errorf("mutant %s (%s) at output.tier.value: state %s, want %s — "+
				"an out-of-cone unknown blocked the equality claim",
				mutant.ID, mutant.Operator, mutant.State, report.Unobservable)
		}
	}

	if !found {
		t.Fatal("no fingerprint-identical mutant fired at output.tier.value; the fixture lost its point")
	}
}

// TestOwnResourceUnknownsStayInCone pins the same-resource attribute union:
// a fingerprint-identical mutant inside a resource keeps its own computed
// unknowns in-cone in plan mode, so the verdict stays
// indeterminate-unknown-values — and the evidence names the in-cone unknowns.
func TestOwnResourceUnknownsStayInCone(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "redundant-in-resource")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	found := false

	for _, mutant := range result.Mutants {
		if mutant.State != report.Survived || mutant.Resource != "terraform_data.app" {
			continue
		}

		found = true

		if mutant.Verdict == nil || mutant.Verdict.Diagnosis != report.IndeterminateUnknownValues {
			t.Fatalf("mutant %s (%s): expected indeterminate-unknown-values, got %+v",
				mutant.ID, mutant.Operator, mutant.Verdict)
		}

		if len(mutant.Verdict.Evidence.UnknownPaths) == 0 {
			t.Fatalf("mutant %s: the report must name the in-cone unknowns behind the verdict",
				mutant.ID)
		}

		for _, unknown := range mutant.Verdict.Evidence.UnknownPaths {
			if !strings.HasPrefix(unknown, "terraform_data.app") {
				t.Fatalf("mutant %s: unknown %q is outside the cone yet blocked the claim",
					mutant.ID, unknown)
			}
		}
	}

	if !found {
		t.Fatal("no surviving mutant inside terraform_data.app; the fixture lost its point")
	}
}

// TestStaticUnobservableEqualsTheExecutedVerdict is the shortcut's control:
// the same module classified with the shortcut and with it disabled reaches
// identical verdicts, and the shortcut's mutants demonstrably skipped
// execution.
func TestStaticUnobservableEqualsTheExecutedVerdict(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "static-unobservable")

	static, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("static run: %v", err)
	}

	control := baseConfig(t, module)
	control.DisableStaticUnobservable = true

	executed, err := engine.Run(t.Context(), control)
	if err != nil {
		t.Fatalf("control run: %v", err)
	}

	verified := false

	for _, mutant := range static.Mutants {
		if mutant.Site != "local.unused" {
			continue
		}

		verified = true

		if mutant.State != report.Unobservable {
			t.Fatalf("shortcut gave local.unused mutant %s state %s, want %s",
				mutant.ID, mutant.State, report.Unobservable)
		}

		if mutant.ExecutedRuns != 0 || len(mutant.Runs) != 0 {
			t.Fatalf("a statically classified mutant executed anyway: %+v", mutant.Runs)
		}

		reference, found := executed.MutantByID(mutant.ID)
		if !found {
			t.Fatalf("the control run lost mutant %s", mutant.ID)
		}

		if reference.State != report.Unobservable {
			t.Fatalf("the executed verdict for %s is %s; the shortcut claims %s — not equal",
				mutant.ID, reference.State, report.Unobservable)
		}

		if reference.ExecutedRuns == 0 {
			t.Fatal("the control run did not actually execute the mutant, so it proves nothing")
		}
	}

	if !verified {
		t.Fatal("no mutant fired at local.unused; the control fixture lost its point")
	}
}

// TestTheContractFixtureClassifiesIdenticallyUnderTheShortcut is the C2
// structural guard: validations, preconditions, postconditions and checks are
// StructurallyUnassertable or killable, never statically Unobservable, and
// the shortcut must not move a single verdict in the M2 contract fixture.
func TestTheContractFixtureClassifiesIdenticallyUnderTheShortcut(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "contract")

	static, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("static run: %v", err)
	}

	control := baseConfig(t, module)
	control.DisableStaticUnobservable = true

	executed, err := engine.Run(t.Context(), control)
	if err != nil {
		t.Fatalf("control run: %v", err)
	}

	assertVerdictInvariance(t, executed, static)

	for _, mutant := range static.Mutants {
		if mutant.State == report.Unobservable && mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s at %s) was statically classified in the contract fixture; "+
				"the structural guard must keep the shortcut away from contract constructs",
				mutant.ID, mutant.Operator, mutant.Site)
		}
	}
}
