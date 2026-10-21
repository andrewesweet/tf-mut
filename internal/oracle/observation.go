package oracle

import (
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Measurement is everything the engine measured about one mutant, plus the
// fail-closed adapters over the engine's measured sources: the single value
// NewObservation builds an Observation from. Every field names what it
// carries, so the caller assembles the value by name instead of knowing the
// order the oracle reads it in.
//
// Optional evidence retains its zero value until measured: the validate verdict
// is recorded when NeedsValidate requires it, and PhaseTwoDecided marks a
// completed fingerprint comparison. The engine must wire all four adapters
// before requesting decisions that use them; nil adapters have no fallback.
// The engine keeps every I/O duty — running Terraform, canonicalising payloads,
// building the graph cone — and every decision over these facts belongs to the
// oracle.
type Measurement struct {
	// Phase one: what the executed runs reported.
	FailedRuns   bool
	ErroredRuns  bool
	ExecutedRuns int
	TimedOut     bool
	Budget       time.Duration
	Diagnostics  []Diagnostic

	// The validate verdict: recorded where the engine honoured NeedsValidate.
	// Until then no verdict that needs it can be judged.
	// InvalidDiagnostics merges the test and validate diagnostics in sorted order.
	ValidateValid      bool
	InvalidDiagnostics []Diagnostic

	// Phase two: what the fingerprint comparison measured — the mutant's
	// operator, the masked delta against the baseline, the mask it was
	// compared under, the unknown paths the payload carries, and the
	// attributes that differed across the mutant's own two runs once the
	// volatility re-run recorded its comparison.
	PhaseTwoDecided bool
	Operator        mutation.Operator
	Delta           fingerprint.Delta
	Mask            fingerprint.Mask
	Unknowns        []string
	Unstable        []string

	// InCone reports whether a payload address lies in the mutation's forward
	// cone. An unmappable site must answer true for every address, preserving
	// the whole-payload unknown floor.
	InCone func(payloadAddress string) bool
	// Reads adapts the output and local closure: what the closure concludes
	// about an address's assertions.
	Reads func(address string) discovery.Reach
	// ComputedAttributes adapts the provider schemas: whether an attribute is
	// computed, and whether the schema knows the attribute at all.
	ComputedAttributes func(kind, resource, attribute string) (computed, known bool)
	// Suspicious adapts the static impure scan over the mutant's own syntax:
	// whether the scan marks a resource suspicious.
	Suspicious func(resource string) bool
}

// Observation is everything the engine measured about one mutant, assembled
// for the precedence table to judge. It is built only through NewObservation,
// from one Measurement value; every decision over those facts — the
// predicates below and Judge's precedence table — belongs to the oracle.
type Observation struct {
	measured Measurement
}

// NewObservation assembles an observation from the one Measurement the
// engine measured: what the executed runs reported, the validate verdict
// where it was owed, the fingerprint comparison where phase two decided, and
// the fail-closed adapters over the engine's measured sources. The engine
// calls it wherever it asks the oracle a question or requests a judgement.
func NewObservation(measured Measurement) Observation {
	return Observation{measured: measured}
}

// DeadlineExceeded reports whether a deadline recorded anywhere in the
// measurement — phase one's budget or a survivor-side run's — has fired. The
// engine records the fact on the verdict so the report shows the deadline
// behind whatever verdict the precedence table earned.
func (o Observation) DeadlineExceeded() bool {
	return o.measured.TimedOut
}

// NeedsValidate reports whether the precedence table needs the validate
// verdict: after a run-level error or a zero-run result, unless an assertion
// already caught the mutant. The engine records the verdict in the
// measurement before calling Judge.
func (o Observation) NeedsValidate() bool {
	if o.measured.FailedRuns {
		return false
	}

	return o.measured.ErroredRuns || o.measured.ExecutedRuns == 0
}

// NeedsPhaseTwo reports the phase-two decision: the fingerprint stage is
// owed only for a phase-one survivor — the state Judge leaves when phase one
// is out of decisions — and only where phase two has not already decided.
// Since 2.17.0 the engine answers an affirmative from the survivor's own
// verbose stream rather than a second invocation.
func (o Observation) NeedsPhaseTwo(outcome Outcome) bool {
	return outcome.State() == StateSurvived && !o.measured.PhaseTwoDecided
}

// NoVerdictPossible reports that no run block executed, so no verdict is
// possible. A refusal at the engine, never a verdict: a filter that matches
// nothing exits zero and must never be read as survival.
func (o Observation) NoVerdictPossible() bool {
	return o.measured.ExecutedRuns == 0
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
	if len(o.measured.Delta.Changes) == 0 {
		return false
	}

	if o.computedConfined() {
		return true
	}

	for _, address := range o.measured.Delta.Addresses() {
		if o.measured.Suspicious(resourceOf(address)) {
			return true
		}
	}

	return false
}
