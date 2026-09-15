package oracle

import (
	"fmt"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// The constructors are the only way an Outcome exists. Each survivor
// constructor's parameters ARE its diagnosis's required evidence subset; the
// terminal constructors take no diagnosis, because a diagnosis names why a
// mutant that every run passed survived and a terminal outcome is not one.

// SurvivedIndeterminateUnknowns records a fingerprint-identical survivor whose
// payload carries unknown values, so equality cannot be proven.
func SurvivedIndeterminateUnknowns(unknowns []string, mask fingerprint.Mask) Outcome {
	return Outcome{
		state:     StateSurvived,
		diagnosis: IndeterminateUnknownValues,
		message: fmt.Sprintf(
			"every selected run produced the same plan or state, but %d value(s) are still unknown, "+
				"so the tool cannot prove no assertion could tell the mutant apart",
			len(unknowns),
		),
		fix: "run this module in apply mode, or supply inputs that make the unknown values known, " +
			"and re-run: the oracle reaches full power only over a fully-known payload",
		evidence: Evidence{
			unknownPaths:       unknowns,
			volatileComponents: mask.Paths(),
		},
	}
}

// SurvivedIndeterminateVolatility records a survivor whose delta remained
// undecidable after the mutant was re-run.
func SurvivedIndeterminateVolatility(delta fingerprint.Delta, mask fingerprint.Mask, unstable []string) Outcome {
	// Where the mutant was not re-run — the baseline's own mask could not
	// decompose a value — the undecidable paths are the evidence. The field is
	// never empty: a diagnosis the reader cannot act on is the failure this
	// milestone exists to avoid.
	if len(unstable) == 0 {
		unstable = mask.Undecidables()
	}

	return Outcome{
		state:     StateSurvived,
		diagnosis: IndeterminateVolatility,
		message: "the comparison could not be made soundly: values moved between runs in a way " +
			"the mask could not decompose, so the fingerprint is indeterminate and is never " +
			"treated as identical",
		fix: "pin the volatile values — a mock default, a fixed input, or a deterministic " +
			"function such as uuidv5 — and re-run",
		evidence: Evidence{
			delta:              delta.Changes,
			volatileComponents: mask.Paths(),
			unstableAttributes: unstable,
		},
	}
}

// SurvivedWeakAssertion records a survivor an assertion reads, directly or
// through an output or local, yet does not catch.
func SurvivedWeakAssertion(
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	address string,
	closure discovery.Reach,
) Outcome {
	return Outcome{
		state:     StateSurvived,
		diagnosis: WeakAssertion,
		message: fmt.Sprintf(
			"an assertion reads %s, directly or through an output or local, and still passed: "+
				"the assertion is too loose to catch this change", address,
		),
		fix: "tighten the assertion at " + closure.Assertion.Location() +
			" so that it compares the value the mutation changed",
		evidence: Evidence{
			delta:              delta.Changes,
			volatileComponents: mask.Paths(),
			assertion:          closure.Assertion.Location(),
			closureVerdict:     "read through the output and local closure",
		},
	}
}

// SurvivedNoAssertion records a survivor the output and local closure proves
// no assertion reads.
func SurvivedNoAssertion(delta fingerprint.Delta, mask fingerprint.Mask) Outcome {
	return Outcome{
		state:     StateSurvived,
		diagnosis: NoAssertion,
		message: "the mutant changed the plan or state and the output and local closure shows " +
			"no assertion reads any of the changed addresses",
		fix: "add an assertion over " + strings.Join(delta.Addresses(), ", "),
		evidence: Evidence{
			delta:              delta.Changes,
			volatileComponents: mask.Paths(),
			closureVerdict:     "no assertion reads any delta address",
		},
	}
}

// SurvivedUnasserted records a survivor whose closure was defeated, so weak
// and absent assertions cannot be honestly told apart.
func SurvivedUnasserted(
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	address string,
	closure discovery.Reach,
) Outcome {
	return Outcome{
		state:     StateSurvived,
		diagnosis: Unasserted,
		message: fmt.Sprintf(
			"the mutant changed %s, and an assertion reads it only through a %s: "+
				"whether that assertion is weak or absent cannot be decided honestly",
			address, closure.Construct,
		),
		fix: "assert on " + address + " directly, so that the answer stops depending on a projection",
		evidence: Evidence{
			delta:              delta.Changes,
			volatileComponents: mask.Paths(),
			assertion:          closure.Assertion.Location(),
			closureVerdict:     "defeated",
			defeatedBy:         closure.Construct,
		},
	}
}

// StructurallyUnassertable records a fingerprint-identical mutant of a
// construct with no plan or state projection.
//
// No diagnosis: diagnoses exist only for survivors, and this mutant is
// StructurallyUnassertable. The finding is still actionable, so it keeps its
// message and its fix.
func StructurallyUnassertable(operator mutation.Operator) Outcome {
	entry, _ := mutation.Describe(operator)

	return Outcome{
		state: StateStructurallyUnassertable,
		message: "the mutated construct has no projection into a plan or a state, " +
			"so no assertion over either could ever catch it",
		fix:      entry.Fix,
		evidence: Evidence{closureVerdict: "no plan or state projection"},
	}
}

// Unobservable records a fingerprint-identical mutant of a construct that
// projects, proven over a fully-known payload: no assertion could distinguish
// it under the current inputs.
func Unobservable(mask fingerprint.Mask) Outcome {
	return Outcome{
		state: StateUnobservable,
		message: "every selected run produced an identical plan or state over a fully-known " +
			"payload, so no assertion could distinguish this mutant under the current inputs",
		fix: "either the construct is genuinely redundant, or no run block supplies input " +
			"where it matters: add a run block with different variables, or accept the mutant",
		evidence: Evidence{volatileComponents: mask.Paths()},
	}
}
