package oracle

import (
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Judge assigns a mutant's final state and, for a survivor, its single
// diagnosis by the normative precedence. Each branch supplies the evidence
// required by its constructor. The engine projects the outcome onto the
// published report at the boundary.
//
// The table judges in two phases, in order. Phase one decides from what the
// executed runs reported; a phase-one survivor is the one state it cannot
// decide, and phase two — the fingerprint comparison the engine was told to
// run by Observation.NeedsPhaseTwo — decides what survival means.
func Judge(o Observation) Outcome {
	// Killed is checked before Invalid even though the precedence table ranks
	// Invalid first. The two cannot both apply: a statically invalid
	// configuration never reaches an assertion, so it cannot produce a run that
	// reports fail. Checking here keeps the M11 speed win — the killed majority
	// never pays for validate — without changing any verdict.
	if o.failedRuns {
		return newKilled()
	}

	if o.erroredRuns || o.executedRuns == 0 {
		// validate is the sole discriminator between Invalid and
		// KilledByError, and the engine ran it because NeedsValidate said the
		// branch was reachable.
		if !o.validateValid {
			return newInvalid(o.invalidDiagnostics)
		}

		if o.erroredRuns {
			return newKilledByError(o.diagnostics)
		}
	}

	if o.timedOut {
		return newTimedOut(o.budget)
	}

	// A zero-run result that reaches the fallthrough below is provisional.
	// The engine refuses it through NoVerdictPossible before projecting a survival.

	// Phase one is out of decisions: the mutant survived everything phase one
	// can see, and phase two decides what that means.
	if !o.phaseTwoDecided {
		return newSurvivedPhaseOne()
	}

	// The path-scoped unknown rule (M3a.2): an unknown blocks the equality
	// claim iff its path lies in the mutation's forward cone, under the
	// fail-closed adapters — an unmappable site keeps the whole-payload
	// floor, an unmappable unknown counts as in-cone.
	unknowns := o.blockingUnknowns()
	operator := o.operator

	if proven(o.delta) {
		return o.diagnoseDelta()
	}

	// No difference could be proven.
	//
	// `StructurallyUnassertable` comes first because it claims nothing about
	// equality: the construct has no plan or state projection at all, which is a
	// static property of the construct and true whatever the payload contains.
	// Ordering it below the unknown rule would hide every untested contract
	// behind `indeterminate-unknown-values` in plan mode, where unknowns are
	// almost always present — and story 4 exists precisely so that contract
	// findings count against the reader instead of vanishing.
	//
	// The unknown rule then leads the rest, and is deliberately conservative:
	// cty keeps refinements of unknown values that the plan serialisation
	// discards, so an identical fingerprint over a payload with any unknown in
	// it does not prove unassertability (R2-2).
	switch {
	case !mutation.Projects(operator):
		return newStructurallyUnassertable(operator)
	case len(unknowns) > 0:
		return SurvivedIndeterminateUnknowns(unknowns, o.mask)
	case o.delta.Indeterminate:
		return SurvivedIndeterminateVolatility(o.delta, o.mask, o.unstable)
	default:
		return newUnobservable(o.mask)
	}
}

// blockingUnknowns keeps the unknowns that lie in the mutation's forward
// cone. A site that does not map into the graph falls back to the whole
// payload — every unknown blocks — and an unknown that does not map is
// treated as in-cone by the adapter itself.
func (o Observation) blockingUnknowns() []string {
	blocking := []string{}

	for _, unknown := range o.unknowns {
		if o.inCone(unknown) {
			blocking = append(blocking, unknown)
		}
	}

	return blocking
}

// proven reports a difference the oracle can stand behind.
//
// An undecidable path elsewhere in the payload does not unmake an observed
// change: the change was observed over components the mask left alone, and a
// mutant that moved something the suite could have asserted on is a finding
// whatever else about the payload could not be compared.
func proven(delta fingerprint.Delta) bool {
	return len(delta.Changes) > 0
}

// diagnoseDelta assigns the survivor's single diagnosis for a proven
// difference, by the normative precedence.
func (o Observation) diagnoseDelta() Outcome {
	// The mock-masked diagnosis was withdrawn here (M3, issue #50): a stable
	// apply-mode delta confined to computed-flagged attributes is
	// attributable to the module wherever the attribute is configurable, and
	// a computed-only attribute cannot produce one. The delta falls through
	// to the closure diagnoses, which is what it always was.
	for _, address := range o.delta.Addresses() {
		if closureVerdict := o.reads(address); closureVerdict.Read {
			return SurvivedWeakAssertion(o.delta, o.mask, address, closureVerdict)
		}
	}

	for _, address := range o.delta.Addresses() {
		if closureVerdict := o.reads(address); closureVerdict.Defeated {
			return SurvivedUnasserted(o.delta, o.mask, address, closureVerdict)
		}
	}

	return SurvivedNoAssertion(o.delta, o.mask)
}

// computedConfined reports a delta that lies entirely in attributes the
// provider computes, which is the shape a mock's invented value takes.
func (o Observation) computedConfined() bool {
	addresses := o.delta.Addresses()
	if len(addresses) == 0 || !allAddressed(o.delta) {
		return false
	}

	for _, address := range addresses {
		coordinates, ok := schemaAttribute(address)
		if !ok {
			return false
		}

		computed, known := o.computed(coordinates.kind, coordinates.resource, coordinates.attribute)
		if !known || !computed {
			return false
		}
	}

	return true
}

// allAddressed reports whether every change in the delta names an address.
func allAddressed(delta fingerprint.Delta) bool {
	for _, change := range delta.Changes {
		if change.Address == "" {
			return false
		}
	}

	return true
}

// lookup is the schema coordinates a Terraform address implies.
type lookup struct {
	kind      string
	resource  string
	attribute string
}

// The address roots that are not managed resources.
const (
	dataRoot   = "data"
	outputRoot = "output"
)

// schemaAttribute splits a Terraform address into the schema lookup it
// implies. The grammar is the canonical one's: a data address carries its
// attribute at `data.<type>.<name>.<attribute>`, a managed resource's at
// `<type>.<name>.<attribute>`, and an `output.` root names no schema entry.
//
// The parse is discovery's: the canonical grammar, instance keys and all, is
// the one this package asks, so the oracle's reading of an address cannot
// drift from the graph's and the mutation walker's (SDP review T4).
func schemaAttribute(address string) (lookup, bool) {
	parsed := discovery.ParseAddr(address)

	// A module-qualified address names child-module internals, which a run
	// rooted at this module observes only through its outputs — the same
	// refusal suggest's address adapter makes. It implies no schema lookup
	// here, and no resource this module's volatility scan could flag.
	if len(parsed.ModulePath) > 0 {
		return lookup{}, false
	}

	parts := parsed.Parts

	if len(parts) >= 4 && parts[0] == dataRoot {
		return lookup{kind: dataRoot, resource: parts[1], attribute: parts[3]}, true
	}

	if len(parts) >= 3 && parts[0] != dataRoot && parts[0] != outputRoot {
		return lookup{kind: "resource", resource: parts[0], attribute: parts[2]}, true
	}

	return lookup{kind: "", resource: "", attribute: ""}, false
}

// resourceOf is the resource address an attribute address belongs to: the
// `data.<type>.<name>` a data address carries, or the bare `<type>.<name>` a
// managed resource's does. A module-qualified address belongs to no resource
// of this module, so it names nothing the mutant's volatility scan keys — the
// empty address.
func resourceOf(address string) string {
	parsed := discovery.ParseAddr(address)

	if len(parsed.ModulePath) > 0 {
		return ""
	}

	parts := parsed.Parts

	if len(parts) >= 3 && parts[0] == dataRoot {
		resource, _ := discovery.BlockAddr(dataRoot, parts[1:3])

		return resource
	}

	if len(parts) >= 2 { //nolint:mnd // the canonical grammar's bare `<type>.<name>` resource arity.
		resource, _ := discovery.BlockAddr("resource", parts[:2])

		return resource
	}

	return address
}
