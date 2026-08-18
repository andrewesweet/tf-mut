package engine

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// oracle answers the only question phase one cannot: whether a mutant that
// every run passed produced any observable difference at all, and if so what
// the suite would have had to do to notice.
type oracle struct {
	plan executionPlan
}

// observe runs phase two for one phase-one survivor and assigns its final state
// and diagnosis.
//
// Phase two exists because `-verbose` embeds the whole provider schema in every
// per-run message — 20,288 times the output volume, measured — so only the
// non-killed minority may pay for it.
func (o oracle) observe(
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
func (o oracle) fingerprintRun(
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
func (o oracle) needsRerun(index int, delta fingerprint.Delta) bool {
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
func (o oracle) mutantScan(index int) discovery.VolatilityScan {
	mutant := o.plan.generated[index]

	sources := maps.Clone(o.plan.prepared.sources)
	sources[filepath.Join(o.plan.configuration.ClosureRoot, mutant.File)] = mutant.Mutated

	return o.plan.configuration.ScanVolatility(sources)
}

// computedConfined reports a delta that lies entirely in attributes the
// provider computes, which is the shape a mock's invented value takes.
func (o oracle) computedConfined(delta fingerprint.Delta) bool {
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
// the normative precedence.
func (o oracle) classify(
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
		verdict.State = report.Survived
		verdict.Verdict = o.diagnoseDelta(delta, mask)

		return verdict
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
		verdict.State = report.StructurallyUnassertable
		verdict.Verdict = unassertableVerdict(operator)
	case len(unknowns) > 0:
		verdict.State = report.Survived
		verdict.Verdict = unknownVerdict(unknowns, mask)
	case delta.Indeterminate:
		verdict.State = report.Survived
		verdict.Verdict = volatilityVerdict(delta, mask, unstable)
	default:
		verdict.State = report.Unobservable
		verdict.Verdict = unobservableVerdict(mask)
	}

	return verdict
}

// blockingUnknowns keeps the unknowns that lie in the mutation's forward
// cone. A site that does not map into the graph falls back to the whole
// payload — every unknown blocks — and an unknown that does not map is
// treated as in-cone by the adapter itself.
func (o oracle) blockingUnknowns(verdict report.Mutant, unknowns []string) []string {
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

func (o oracle) diagnoseDelta(delta fingerprint.Delta, mask fingerprint.Mask) *report.Verdict {
	// The mock-masked diagnosis was withdrawn here (M3, issue #50): a stable
	// apply-mode delta confined to computed-flagged attributes is
	// attributable to the module wherever the attribute is configurable, and
	// a computed-only attribute cannot produce one. The delta falls through
	// to the closure diagnoses, which is what it always was.
	for _, address := range delta.Addresses() {
		if closureVerdict := o.plan.closure.Reads(address); closureVerdict.Read {
			return weakAssertionVerdict(delta, mask, address, closureVerdict)
		}
	}

	for _, address := range delta.Addresses() {
		if closureVerdict := o.plan.closure.Reads(address); closureVerdict.Defeated {
			return unassertedVerdict(delta, mask, address, closureVerdict)
		}
	}

	return noAssertionVerdict(delta, mask)
}

func unknownVerdict(unknowns []string, mask fingerprint.Mask) *report.Verdict {
	return &report.Verdict{
		Diagnosis: report.IndeterminateUnknownValues,
		Message: fmt.Sprintf(
			"every selected run produced the same plan or state, but %d value(s) are still unknown, "+
				"so the tool cannot prove no assertion could tell the mutant apart",
			len(unknowns),
		),
		Fix: "run this module in apply mode, or supply inputs that make the unknown values known, " +
			"and re-run: the oracle reaches full power only over a fully-known payload",
		Evidence: report.Evidence{ //nolint:exhaustruct // each diagnosis names its own subset.
			UnknownPaths:       unknowns,
			VolatileComponents: mask.Paths(),
		},
	}
}

func volatilityVerdict(delta fingerprint.Delta, mask fingerprint.Mask, unstable []string) *report.Verdict {
	// Where the mutant was not re-run — the baseline's own mask could not
	// decompose a value — the undecidable paths are the evidence. The field is
	// never empty: a diagnosis the reader cannot act on is the failure this
	// milestone exists to avoid.
	if len(unstable) == 0 {
		unstable = mask.Undecidables()
	}

	return &report.Verdict{
		Diagnosis: report.IndeterminateVolatility,
		Message: "the comparison could not be made soundly: values moved between runs in a way " +
			"the mask could not decompose, so the fingerprint is indeterminate and is never " +
			"treated as identical",
		Fix: "pin the volatile values — a mock default, a fixed input, or a deterministic " +
			"function such as uuidv5 — and re-run",
		Evidence: report.Evidence{ //nolint:exhaustruct // each diagnosis names its own subset.
			Delta:              changes(delta),
			VolatileComponents: mask.Paths(),
			UnstableAttributes: unstable,
		},
	}
}

func unassertableVerdict(operator mutation.Operator) *report.Verdict {
	entry, _ := mutation.Describe(operator)

	// No diagnosis: diagnoses exist only for survivors, and this mutant is
	// StructurallyUnassertable. The finding is still actionable, so it keeps its
	// message and its fix.
	return &report.Verdict{
		Diagnosis: "",
		Message: "the mutated construct has no projection into a plan or a state, " +
			"so no assertion over either could ever catch it",
		Fix:      entry.Fix,
		Evidence: report.Evidence{ClosureVerdict: "no plan or state projection"}, //nolint:exhaustruct // no delta exists.
	}
}

func unobservableVerdict(mask fingerprint.Mask) *report.Verdict {
	return &report.Verdict{
		Diagnosis: "",
		Message: "every selected run produced an identical plan or state over a fully-known " +
			"payload, so no assertion could distinguish this mutant under the current inputs",
		Fix: "either the construct is genuinely redundant, or no run block supplies input " +
			"where it matters: add a run block with different variables, or accept the mutant",
		Evidence: report.Evidence{VolatileComponents: mask.Paths()}, //nolint:exhaustruct // no delta exists.
	}
}

func weakAssertionVerdict(
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	address string,
	closure discovery.Reach,
) *report.Verdict {
	return &report.Verdict{
		Diagnosis: report.WeakAssertion,
		Message: fmt.Sprintf(
			"an assertion reads %s, directly or through an output or local, and still passed: "+
				"the assertion is too loose to catch this change", address,
		),
		Fix: "tighten the assertion at " + closure.Assertion.Location() +
			" so that it compares the value the mutation changed",
		Evidence: report.Evidence{ //nolint:exhaustruct // each diagnosis names its own subset.
			Delta:              changes(delta),
			VolatileComponents: mask.Paths(),
			Assertion:          closure.Assertion.Location(),
			ClosureVerdict:     "read through the output and local closure",
		},
	}
}

func noAssertionVerdict(delta fingerprint.Delta, mask fingerprint.Mask) *report.Verdict {
	return &report.Verdict{
		Diagnosis: report.NoAssertion,
		Message: "the mutant changed the plan or state and the output and local closure shows " +
			"no assertion reads any of the changed addresses",
		Fix: "add an assertion over " + strings.Join(delta.Addresses(), ", "),
		Evidence: report.Evidence{ //nolint:exhaustruct // each diagnosis names its own subset.
			Delta:              changes(delta),
			VolatileComponents: mask.Paths(),
			ClosureVerdict:     "no assertion reads any delta address",
		},
	}
}

func unassertedVerdict(
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	address string,
	closure discovery.Reach,
) *report.Verdict {
	return &report.Verdict{
		Diagnosis: report.Unasserted,
		Message: fmt.Sprintf(
			"the mutant changed %s, and an assertion reads it only through a %s: "+
				"whether that assertion is weak or absent cannot be decided honestly",
			address, closure.Construct,
		),
		Fix: "assert on " + address + " directly, so that the answer stops depending on a projection",
		Evidence: report.Evidence{ //nolint:exhaustruct // each diagnosis names its own subset.
			Delta:              changes(delta),
			VolatileComponents: mask.Paths(),
			Assertion:          closure.Assertion.Location(),
			ClosureVerdict:     "defeated",
			DefeatedBy:         closure.Construct,
		},
	}
}

// maxReportedChanges bounds the delta a report carries. A mutant that empties a
// resource changes every attribute of it, and a hundred lines of evidence
// serves nobody. Confirmed by measurement (M3c): only 4.3% of real survivors
// saturate this cap, all of them whole-resource mutants.
const maxReportedChanges = 20

func changes(delta fingerprint.Delta) []report.Change {
	converted := make([]report.Change, 0, min(len(delta.Changes), maxReportedChanges))

	for index, change := range delta.Changes {
		if index >= maxReportedChanges {
			break
		}

		converted = append(converted, report.Change{
			Run:     change.Run,
			Path:    change.Path,
			Address: change.Address,
			// The values are withheld where Terraform marks them sensitive
			// (2.2.0). The reader still learns that the path changed, which is
			// the whole evidential content of a delta; the value itself is the
			// one thing a report must not carry, because every reporter — the
			// JSON document, the SARIF artefact, the job step summary — would
			// carry it too.
			Baseline: withheld(change.Sensitive, change.Baseline),
			Mutant:   withheld(change.Sensitive, change.Mutant),
			// Carried through deliberately: the suggestion engine's
			// sensitivity predicate is decided from this flag, and dropping it
			// here would let a secret reach a generated expression.
			Sensitive: change.Sensitive,
		})
	}

	return converted
}

// SensitiveWithheld is what a report carries in place of a sensitive value. It
// is deliberately not a canonical rendering: every rendering of a string starts
// with a quote, so nothing a module could hold can collide with it.
const SensitiveWithheld = "(sensitive value withheld)"

// withheld replaces a sensitive rendering, and leaves an absent one absent so
// that "the path was not there" stays distinguishable from "it was withheld".
func withheld(sensitive bool, rendering string) string {
	if !sensitive || rendering == "" {
		return rendering
	}

	return SensitiveWithheld
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
