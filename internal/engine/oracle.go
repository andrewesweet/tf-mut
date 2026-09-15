package engine

import (
	"context"
	"maps"
	"path/filepath"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// executionOracle answers the only question phase one cannot: whether a mutant
// that every run passed produced any observable difference at all, and if so
// what the suite would have had to do to notice.
type executionOracle struct {
	plan executionPlan
}

// observe runs phase two for one phase-one survivor and assigns its final state
// and diagnosis.
//
// Phase two exists because `-verbose` embeds the whole provider schema in every
// per-run message — 20,288 times the output volume, measured — so only the
// non-killed minority may pay for it.
func (o executionOracle) observe(
	ctx context.Context,
	built sandbox.Sandbox,
	index int,
	verdict report.Mutant,
) (report.Mutant, *report.ExecutionError) {
	payloads, outcomes, err := o.fingerprintRun(ctx, built)
	if err != nil {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "the fingerprint run could not be evaluated: " + err.Error(),
		}
	}

	verdict.Runs = append(verdict.Runs, outcomes...)

	mask := o.plan.prepared.mask
	delta := fingerprint.Compare(mask, o.plan.prepared.payloads, payloads)
	unstable := []string{}

	// A run block the baseline fingerprinted and the mutant did not is an
	// operational condition, not a verdict: phase one reported every run as
	// passing, so a missing payload means the two phases disagree and neither
	// can be trusted over the other.
	if len(delta.MissingRuns) > 0 {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message: "the fingerprint run produced no payload for " +
				strings.Join(delta.MissingRuns, ", ") + ", which phase one reported as passing",
		}
	}

	if o.needsRerun(index, delta) {
		second, _, rerunErr := o.fingerprintRun(ctx, built)
		if rerunErr != nil {
			return verdict, &report.ExecutionError{
				MutantID: verdict.ID,
				Site:     verdict.Site,
				Message:  "the volatility re-run could not be evaluated: " + rerunErr.Error(),
			}
		}

		mutantMask := fingerprint.Derive(payloads, second)
		unstable = mutantMask.Paths()
		mask = mask.MergeMutantVolatility(mutantMask)
		delta = fingerprint.Compare(mask, o.plan.prepared.payloads, payloads)
	}

	return o.classify(verdict, payloads, delta, mask, unstable), nil
}

// fingerprintRun executes the verbose phase and projects its payloads.
func (o executionOracle) fingerprintRun(
	ctx context.Context,
	built sandbox.Sandbox,
) ([]fingerprint.Payload, []report.RunOutcome, error) {
	result, err := o.plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: o.plan.configuration.TestDirRelative(),
		Filters:       o.plan.config.TestSelection,
		Verbose:       true,
		Timeout:       0,
	})
	if err != nil {
		return nil, nil, err
	}

	payloads, err := fingerprint.Canonicalise(result.Payloads)
	if err != nil {
		return nil, nil, err
	}

	outcomes := make([]report.RunOutcome, 0, len(result.Runs))
	for _, run := range result.Runs {
		outcomes = append(outcomes, report.RunOutcome{
			File: run.File, Run: run.Run, Phase: phaseTwo, Status: run.Status,
		})
	}

	return payloads, outcomes, nil
}

// The two execution phases, recorded per run so that a reader can confirm the
// verbose phase ran only where the design says it should.
const (
	phaseOne = 1
	phaseTwo = 2
)

// needsRerun applies the mutation-introduced volatility rule (spec review C4).
//
// A mutation can expose a volatile path or create mock instances the baseline
// never observed, so the baseline diff cannot mask them. Where the delta is
// confined to attributes the provider fills in, or touches a resource the
// static impure scan over the *mutant's* own syntax marks suspicious, the
// mutant is run once more and the difference between its two runs is masked.
func (o executionOracle) needsRerun(index int, delta fingerprint.Delta) bool {
	if len(delta.Changes) == 0 {
		return false
	}

	if o.computedConfined(delta) {
		return true
	}

	scan := o.mutantScan(index)
	for _, address := range delta.Addresses() {
		if scan.Suspicious(resourceOf(address)) {
			return true
		}
	}

	return false
}

// mutantScan runs the static impure scan over the mutant's own syntax.
func (o executionOracle) mutantScan(index int) discovery.VolatilityScan {
	mutant := o.plan.generated[index]

	sources := maps.Clone(o.plan.prepared.sources)
	sources[filepath.Join(o.plan.configuration.ClosureRoot, mutant.File)] = mutant.Mutated

	return o.plan.configuration.ScanVolatility(sources)
}

// computedConfined reports a delta that lies entirely in attributes the
// provider computes, which is the shape a mock's invented value takes.
func (o executionOracle) computedConfined(delta fingerprint.Delta) bool {
	addresses := delta.Addresses()
	if len(addresses) == 0 || !allAddressed(delta) {
		return false
	}

	for _, address := range addresses {
		coordinates, ok := schemaAttribute(address)
		if !ok {
			return false
		}

		computed, known := o.plan.prepared.schemas.Computed(
			coordinates.kind, coordinates.resource, coordinates.attribute,
		)
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

// classify assigns the final state and, for a survivor, its single diagnosis by
// the normative precedence. The precedence table is the constructors' only
// caller: each branch names the evidence its diagnosis requires through the
// constructor's parameters, and the outcome is projected onto the published
// report at the boundary.
func (o executionOracle) classify(
	verdict report.Mutant,
	payloads []fingerprint.Payload,
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	unstable []string,
) report.Mutant {
	// The path-scoped unknown rule (M3a.2): an unknown blocks the equality
	// claim iff its path lies in the mutation's forward cone, under the
	// fail-closed adapters — an unmappable site keeps the whole-payload
	// floor, an unmappable unknown counts as in-cone.
	unknowns := o.blockingUnknowns(verdict, fingerprint.Unknowns(payloads))
	operator := mutation.Operator(verdict.Operator)

	if proven(delta) {
		return project(verdict, o.diagnoseDelta(delta, mask))
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
		return project(verdict, oracle.StructurallyUnassertable(operator))
	case len(unknowns) > 0:
		return project(verdict, oracle.SurvivedIndeterminateUnknowns(unknowns, mask))
	case delta.Indeterminate:
		return project(verdict, oracle.SurvivedIndeterminateVolatility(delta, mask, unstable))
	default:
		return project(verdict, oracle.Unobservable(mask))
	}
}

// blockingUnknowns keeps the unknowns that lie in the mutation's forward
// cone. A site that does not map into the graph falls back to the whole
// payload — every unknown blocks — and an unknown that does not map is
// treated as in-cone by the adapter itself.
func (o executionOracle) blockingUnknowns(verdict report.Mutant, unknowns []string) []string {
	cone, ok := o.plan.graph.SiteCone(verdict.Module, verdict.Site)
	if !ok {
		return unknowns
	}

	blocking := []string{}

	for _, unknown := range unknowns {
		if cone.ContainsPayloadAddress(unknown) {
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

func (o executionOracle) diagnoseDelta(delta fingerprint.Delta, mask fingerprint.Mask) oracle.Outcome {
	// The mock-masked diagnosis was withdrawn here (M3, issue #50): a stable
	// apply-mode delta confined to computed-flagged attributes is
	// attributable to the module wherever the attribute is configurable, and
	// a computed-only attribute cannot produce one. The delta falls through
	// to the closure diagnoses, which is what it always was.
	for _, address := range delta.Addresses() {
		if closureVerdict := o.plan.closure.Reads(address); closureVerdict.Read {
			return oracle.SurvivedWeakAssertion(delta, mask, address, closureVerdict)
		}
	}

	for _, address := range delta.Addresses() {
		if closureVerdict := o.plan.closure.Reads(address); closureVerdict.Defeated {
			return oracle.SurvivedUnasserted(delta, mask, address, closureVerdict)
		}
	}

	return oracle.SurvivedNoAssertion(delta, mask)
}

// lookup is the schema coordinates a Terraform address implies.
type lookup struct {
	kind      string
	resource  string
	attribute string
}

// The address lengths that carry an attribute, by block kind.
const (
	dataAddressParts     = 4
	resourceAddressParts = 3
)

// The address roots that are not managed resources.
const (
	dataRoot   = "data"
	outputRoot = "output"
)

// schemaAttribute splits a Terraform address into the schema lookup it implies.
func schemaAttribute(address string) (lookup, bool) {
	parts := strings.Split(stripInstanceKeys(address), ".")

	if len(parts) >= dataAddressParts && parts[0] == dataRoot {
		return lookup{kind: dataRoot, resource: parts[1], attribute: parts[3]}, true
	}

	if len(parts) >= resourceAddressParts && parts[0] != dataRoot && parts[0] != outputRoot {
		return lookup{kind: "resource", resource: parts[0], attribute: parts[2]}, true
	}

	return lookup{kind: "", resource: "", attribute: ""}, false
}

// resourceOf is the resource address an attribute address belongs to.
func resourceOf(address string) string {
	parts := strings.Split(stripInstanceKeys(address), ".")

	if len(parts) >= resourceAddressParts && parts[0] == dataRoot {
		return strings.Join(parts[:resourceAddressParts], ".")
	}

	if len(parts) >= addressParts {
		return strings.Join(parts[:addressParts], ".")
	}

	return address
}

// addressParts is the length of a bare `<type>.<name>` address.
const addressParts = 2

func stripInstanceKeys(address string) string {
	builder := strings.Builder{}
	depth := 0

	for _, letter := range address {
		switch {
		case letter == '[':
			depth++
		case letter == ']':
			depth--
		case depth == 0:
			builder.WriteRune(letter)
		default:
		}
	}

	return builder.String()
}
