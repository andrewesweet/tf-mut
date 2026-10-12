package oracle

import (
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Observation is everything the engine measured about one mutant, assembled
// for the precedence table to judge. Its fields are unexported: the engine
// builds it through the With constructors from its measurements: the mutant's
// operator, the unknown paths, the masked delta and mask,
// the unstable components, the closure's Reach, the phase-one status, the
// diagnostics and the validate verdict — and every decision over those facts
// belongs to the oracle. The engine keeps every I/O duty: running Terraform,
// canonicalising payloads, building the graph cone. The four func fields are
// the fail-closed adapters over the engine's measured sources: the graph
// cone, the output and local closure, the provider schemas' computed flags
// and the mutant's own volatility scan.
type Observation struct {
	// Phase one: what the executed runs reported.
	failedRuns   bool
	erroredRuns  bool
	executedRuns int
	timedOut     bool
	budget       time.Duration
	diagnostics  []Diagnostic

	// The validate verdict, recorded by WithValidateVerdict when the engine
	// honoured NeedsValidate. Until then no verdict that needs it can be
	// judged.
	validateValid      bool
	invalidDiagnostics []Diagnostic

	// Phase two: what the fingerprint comparison measured, recorded by
	// WithPhaseTwo and WithSecondRun.
	phaseTwoDecided bool
	operator        mutation.Operator
	delta           fingerprint.Delta
	mask            fingerprint.Mask
	unstable        []string
	unknowns        []string

	// The fail-closed adapters over the engine's measured sources.
	inCone     func(payloadAddress string) bool
	reads      func(address string) discovery.Reach
	computed   func(kind, resource, attribute string) (computed, known bool)
	suspicious func(resource string) bool
}

// NewObservation starts an observation from what phase one measured: whether
// any run reported fail or error, how many runs executed, whether the run
// exceeded its budget, the budget it was measured against, and the runs'
// diagnostics.
func NewObservation(
	failedRuns, erroredRuns bool,
	executedRuns int,
	timedOut bool,
	budget time.Duration,
	diagnostics []Diagnostic,
) Observation {
	return Observation{
		failedRuns:   failedRuns,
		erroredRuns:  erroredRuns,
		executedRuns: executedRuns,
		timedOut:     timedOut,
		budget:       budget,
		diagnostics:  diagnostics,
	}
}

// WithPhaseTwoDeadline records a phase-two fingerprint run that outlived its
// budget: the comparison it owed the oracle never arrived, and the precedence
// table's deadline row decides instead. A survivor's phase one guarantees no
// failed or errored run, so the deadline is reached with the table otherwise
// clean — the overrun lands as a Timeout counted in the score's denominator.
func (o Observation) WithPhaseTwoDeadline(budget time.Duration) Observation {
	o.timedOut = true
	o.budget = budget

	return o
}

// WithValidateVerdict records the terraform validate verdict. The engine runs
// validate exactly where NeedsValidate says the table needs it and records
// the result here before calling Judge: the diagnostics are the merged,
// ordered list an Invalid claim is made from.
func (o Observation) WithValidateVerdict(valid bool, invalidDiagnostics []Diagnostic) Observation {
	o.validateValid = valid
	o.invalidDiagnostics = invalidDiagnostics

	return o
}

// WithCone adapts the mutation's forward cone to a payload address: whether
// an unknown at the address lies in the cone. An unmappable site keeps the
// whole-payload floor, so the engine's adapter answers true for every
// address there.
func (o Observation) WithCone(inCone func(payloadAddress string) bool) Observation {
	o.inCone = inCone

	return o
}

// WithClosure adapts the output and local closure: what the closure concludes
// about an address's assertions.
func (o Observation) WithClosure(reads func(address string) discovery.Reach) Observation {
	o.reads = reads

	return o
}

// WithComputedAttributes adapts the provider schemas: whether an attribute is
// computed, and whether the schema knows the attribute at all.
func (o Observation) WithComputedAttributes(
	computed func(kind, resource, attribute string) (computed, known bool),
) Observation {
	o.computed = computed

	return o
}

// WithSuspicion adapts the static impure scan over the mutant's own syntax:
// whether the scan marks a resource suspicious.
func (o Observation) WithSuspicion(suspicious func(resource string) bool) Observation {
	o.suspicious = suspicious

	return o
}

// WithPhaseTwo records the first fingerprint comparison: the mutant's
// operator, the masked delta against the baseline, the mask it was compared
// under, and the unknown paths the payload carries.
func (o Observation) WithPhaseTwo(
	operator mutation.Operator,
	delta fingerprint.Delta,
	mask fingerprint.Mask,
	unknowns []string,
) Observation {
	o.phaseTwoDecided = true
	o.operator = operator
	o.delta = delta
	o.mask = mask
	o.unknowns = unknowns

	return o
}

// WithSecondRun records the comparison after the volatility re-run: the
// recomputed delta and mask, and the attributes that differed across the
// mutant's own two runs.
func (o Observation) WithSecondRun(delta fingerprint.Delta, mask fingerprint.Mask, unstable []string) Observation {
	o.delta = delta
	o.mask = mask
	o.unstable = unstable

	return o
}

// DeadlineExceeded reports whether a deadline recorded anywhere in the
// observation — phase one's budget or a survivor-side run's — has fired. The
// engine records the fact on the verdict so the report shows the deadline
// behind whatever verdict the precedence table earned.
func (o Observation) DeadlineExceeded() bool {
	return o.timedOut
}

// NeedsValidate reports whether the precedence table needs the validate
// verdict: after a run-level error or a zero-run result, unless an assertion
// already caught the mutant. The engine records the verdict with
// WithValidateVerdict before calling Judge.
func (o Observation) NeedsValidate() bool {
	if o.failedRuns {
		return false
	}

	return o.erroredRuns || o.executedRuns == 0
}

// NeedsPhaseTwo reports the phase-two decision: phase two runs only for a
// phase-one survivor — the state Judge leaves when phase one is out of
// decisions — and only where phase two has not already decided.
func (o Observation) NeedsPhaseTwo(outcome Outcome) bool {
	return outcome.State() == StateSurvived && !o.phaseTwoDecided
}

// NoVerdictPossible reports that no run block executed, so no verdict is
// possible. A refusal at the engine, never a verdict: a filter that matches
// nothing exits zero and must never be read as survival.
func (o Observation) NoVerdictPossible() bool {
	return o.executedRuns == 0
}

// NeedsVolatilityRerun applies the mutation-introduced volatility rule (spec
// review C4).
//
// A mutation can expose a volatile path or create mock instances the baseline
// never observed, so the baseline diff cannot mask them. Where the delta is
// confined to attributes the provider fills in, or touches a resource the
// static impure scan over the *mutant's* own syntax marks suspicious, the
// mutant is run once more and the difference between its two runs is masked.
func (o Observation) NeedsVolatilityRerun() bool {
	if len(o.delta.Changes) == 0 {
		return false
	}

	if o.computedConfined() {
		return true
	}

	for _, address := range o.delta.Addresses() {
		if o.suspicious(resourceOf(address)) {
			return true
		}
	}

	return false
}
