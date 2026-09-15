package oracle

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
)

// The refusals a stored record can earn. Each names the invariant the
// constructors enforce and the record broke; the caller treats every one as
// corruption, never as a verdict.
var (
	// ErrUnownedState marks a record naming a state no constructor produces.
	ErrUnownedState = errors.New("not a state this context owns")
	// ErrUnemittedDiagnosis marks a record naming a diagnosis no constructor
	// assigns, or a decided survivor naming none.
	ErrUnemittedDiagnosis = errors.New("not a diagnosis this context emits")
	// ErrTerminalDiagnosis marks a record carrying a diagnosis on a terminal
	// outcome, which no terminal constructor can spell.
	ErrTerminalDiagnosis = errors.New("terminal outcomes carry no diagnosis")
	// ErrEmptyFinding marks a record whose message or fix no constructor
	// would have left empty.
	ErrEmptyFinding = errors.New("carries no message or no fix")
	// ErrUnrecordedEvidence marks a record carrying an evidence field outside
	// the subset its classification records.
	ErrUnrecordedEvidence = errors.New("carries evidence its classification does not record")
	// ErrUnrecordedFinding marks a record carrying a finding where the state's
	// constructors write none.
	ErrUnrecordedFinding = errors.New("carries a finding its state does not record")
	// ErrUnrecordedDecision marks a record whose state is decided from a
	// decision the record does not carry.
	ErrUnrecordedDecision = errors.New("carries no decision its state was recorded from")
)

// The stored-record side of the closed construction set.
//
// A verdict can reach a reader along two paths: freshly classified, through
// the constructors above, or replayed — from the verdict cache's on-disk
// entry, or from the population arithmetic reading the published record back.
// This file closes both paths behind the same invariants: the stored document
// is projected onto a Record, and ParseRecord — the parsing constructor — is
// the only way a Record becomes an Outcome. A stored document the
// constructors could not have produced has no Outcome: the caller treats the
// refusal as corruption, never as a verdict.

// Record is one outcome as a stored record carries it. The application layer
// fills it from the serialised document; the fields are exported because a
// stored record is data, not a construction.
type Record struct {
	// State is the aggregate verdict the stored outcome carried.
	State State
	// Diagnosis is why a stored survivor survived. It is empty for every
	// other state — and for the provisional phase-one survivor, which no
	// phase-two diagnosis was decided for.
	Diagnosis Diagnosis
	// Message and Fix are the stored finding, in the reader's terms.
	Message string
	Fix     string
	// The stored evidence, field for field. Which fields a classification may
	// carry is the constructors' decision, and ParseRecord refuses a record
	// carrying a field outside the subset its state and diagnosis record.
	Delta              []fingerprint.Change
	UnknownPaths       []string
	VolatileComponents []string
	UnstableAttributes []string
	Assertion          string
	ClosureVerdict     string
	DefeatedBy         string
	// Diagnostics are the evaluation failures a KilledByError or Invalid
	// claim is made from. The runs' other diagnostics are observations on the
	// mutant, not outcome evidence, and stay on the mutant.
	Diagnostics []Diagnostic
	// Suppression is the decision an Ignored outcome records.
	Suppression *Suppression
	// Budget is the execution budget a Timeout was measured against.
	Budget time.Duration
}

// ParseRecord rebuilds an Outcome from a stored record, refusing any record
// the constructors could not have produced: a state this context does not
// own, a diagnosis where the state's constructors write none or none where a
// survivor's must have one, a finding left empty where the constructor writes
// one, or evidence outside the subset the stored classification records. A
// refused record produces no value at all, so an illegal combination can no
// more reach a reader from a store than from a fresh classification.
func ParseRecord(record Record) (Outcome, error) {
	subset, verdictless, err := recordedSubset(record)
	if err != nil {
		return Outcome{}, err
	}

	if err := refuseUnrecordedEvidence(record, subset, verdictless); err != nil {
		return Outcome{}, err
	}

	switch record.State {
	case StateSurvived:
		return parseSurvivedRecord(record)
	case StateKilled:
		return Killed(), nil
	case StatePending:
		return Pending(), nil
	case StateKilledByError:
		return KilledByError(record.Diagnostics), nil
	case StateInvalid:
		return Invalid(record.Diagnostics), nil
	case StateTimeout:
		return TimedOut(record.Budget), nil
	case StateNoCoverage:
		return NoCoverage(record.Message), nil
	case StateIgnored:
		return parseIgnoredRecord(record)
	case StateStructurallyUnassertable, StateUnobservable:
		return parseTerminalRecord(record)
	}

	return Outcome{}, fmt.Errorf("oracle: %q: %w", record.State, ErrUnownedState)
}

// parseSurvivedRecord rebuilds a survivor. A decided survivor exists only
// through a constructor that takes a diagnosis, so the record must name
// exactly one emitted diagnosis; the provisional phase-one survivor — the
// state published only where phase two could not run at all — carries no
// diagnosis and no finding, and a record with either is not one this context
// stored. The withdrawn mock-masked spelling exists only in the published
// vocabulary (issue #50), so a record naming it was not stored by one of
// these constructors either.
func parseSurvivedRecord(record Record) (Outcome, error) {
	if record.Diagnosis == "" {
		if err := refuseRecordedFinding(record); err != nil {
			return Outcome{}, err
		}

		return SurvivedPhaseOne(), nil
	}

	if err := refuseEmptyFinding(record); err != nil {
		return Outcome{}, err
	}

	switch record.Diagnosis {
	case IndeterminateUnknownValues, IndeterminateVolatility,
		WeakAssertion, NoAssertion, Unasserted:
		return Outcome{
			state:     StateSurvived,
			diagnosis: record.Diagnosis,
			message:   record.Message,
			fix:       record.Fix,
			evidence:  record.evidence(),
		}, nil
	}

	return Outcome{}, fmt.Errorf("oracle: %q: %w", record.Diagnosis, ErrUnemittedDiagnosis)
}

// parseIgnoredRecord rebuilds an ignored outcome. The suppression is the
// decision the outcome records; a record carrying none was decided by
// nothing, and is refused rather than rebuilt as ignored by default.
func parseIgnoredRecord(record Record) (Outcome, error) {
	if record.Suppression == nil {
		return Outcome{}, fmt.Errorf(
			"oracle: a stored ignored record carries no suppression: %w", ErrUnrecordedDecision,
		)
	}

	return Ignored(*record.Suppression), nil
}

// parseTerminalRecord rebuilds a terminal observable outcome. No terminal
// constructor takes a diagnosis, because a diagnosis names why a mutant that
// every run passed survived and a terminal outcome is not one, so a record
// carrying one — a Killed entry carrying a diagnosis being the canonical
// case — is refused rather than rebuilt.
func parseTerminalRecord(record Record) (Outcome, error) {
	if record.Diagnosis != "" {
		return Outcome{}, fmt.Errorf(
			"oracle: a stored %s outcome carries the diagnosis %q: %w",
			record.State, record.Diagnosis, ErrTerminalDiagnosis,
		)
	}

	if err := refuseEmptyFinding(record); err != nil {
		return Outcome{}, err
	}

	return Outcome{
		state:    record.State,
		message:  record.Message,
		fix:      record.Fix,
		evidence: record.evidence(),
	}, nil
}

// refuseEmptyFinding refuses a record whose finding the constructors could
// not have written: every finding-carrying constructor states both the
// message and the fix.
func refuseEmptyFinding(record Record) error {
	if record.Message == "" || record.Fix == "" {
		return fmt.Errorf("oracle: a stored %s outcome %w", record.State, ErrEmptyFinding)
	}

	return nil
}

// refuseRecordedFinding refuses a state-only record that carries finding
// fields: the constructors for these states write no finding, so a record
// with one was not stored by them.
func refuseRecordedFinding(record Record) error {
	if record.hasFinding() {
		return fmt.Errorf("oracle: a stored %s outcome carries a finding: %w", record.State, ErrUnrecordedFinding)
	}

	return nil
}

// hasFinding reports whether the record carries any part of a published
// finding: the diagnosis aside, which the caller owns, any of the fields a
// Verdict DTO spells.
func (r Record) hasFinding() bool {
	return r.Message != "" || r.Fix != "" || r.hasEvidence()
}

// hasEvidence reports whether the record carries any evidence field.
func (r Record) hasEvidence() bool {
	return r.Delta != nil || r.UnknownPaths != nil || r.VolatileComponents != nil ||
		r.UnstableAttributes != nil || r.Assertion != "" || r.ClosureVerdict != "" ||
		r.DefeatedBy != ""
}

// evidenceSubset records which evidence fields one stored classification
// may carry: exactly the fields its constructor records. verdictless marks
// the states whose constructors write no finding at all.
type evidenceSubset struct {
	verdictless        bool
	delta              bool
	unknownPaths       bool
	volatileComponents bool
	unstableAttributes bool
	assertion          bool
	closureVerdict     bool
	defeatedBy         bool
}

// recordedSubset names the evidence subset the record's state and diagnosis
// record, whether the state writes a finding at all, or refuses a
// classification this context does not emit.
func recordedSubset(record Record) (evidenceSubset, bool, error) {
	switch record.State {
	case StateSurvived:
		return survivedSubset(record.Diagnosis)
	case StateKilled, StatePending, StateKilledByError, StateInvalid,
		StateTimeout, StateIgnored:
		// The states whose constructors write no finding: a Killed entry
		// carrying a diagnosis — the canonical illegal record — is refused
		// here.
		return evidenceSubset{verdictless: true}, true, nil
	case StateNoCoverage:
		// A block-level claim states its reason as the message and derives
		// its fix and closure verdict; the module-level claim is the state
		// alone.
		if record.Message == "" {
			return evidenceSubset{verdictless: true}, true, nil
		}

		return evidenceSubset{closureVerdict: true}, false, nil
	case StateStructurallyUnassertable:
		return evidenceSubset{closureVerdict: true}, false, nil
	case StateUnobservable:
		// The comparison-proven claim carries the mask's components; the
		// statically decided claim carries its closure verdict alone, and no
		// constructor writes both.
		if record.ClosureVerdict != "" && record.VolatileComponents != nil {
			return evidenceSubset{}, false, fmt.Errorf(
				"oracle: a stored unobservable record carries both the mask and the closure verdict: %w",
				ErrUnrecordedEvidence,
			)
		}

		if record.ClosureVerdict != "" {
			return evidenceSubset{closureVerdict: true}, false, nil
		}

		return evidenceSubset{volatileComponents: true}, false, nil
	}

	return evidenceSubset{}, false, fmt.Errorf("oracle: %q: %w", record.State, ErrUnownedState)
}

// survivedSubset names the evidence subset one survivor diagnosis records.
func survivedSubset(diagnosis Diagnosis) (evidenceSubset, bool, error) {
	switch diagnosis {
	case "":
		// The provisional phase-one survivor: no diagnosis, no finding.
		return evidenceSubset{verdictless: true}, true, nil
	case IndeterminateUnknownValues:
		return evidenceSubset{unknownPaths: true, volatileComponents: true}, false, nil
	case IndeterminateVolatility:
		return evidenceSubset{
			delta: true, volatileComponents: true, unstableAttributes: true,
		}, false, nil
	case WeakAssertion:
		return evidenceSubset{
			delta: true, volatileComponents: true, assertion: true, closureVerdict: true,
		}, false, nil
	case NoAssertion:
		return evidenceSubset{delta: true, volatileComponents: true, closureVerdict: true}, false, nil
	case Unasserted:
		return evidenceSubset{
			delta:              true,
			volatileComponents: true,
			assertion:          true,
			closureVerdict:     true,
			defeatedBy:         true,
		}, false, nil
	}

	return evidenceSubset{}, false, fmt.Errorf("oracle: %q: %w", diagnosis, ErrUnemittedDiagnosis)
}

// refuseUnrecordedEvidence refuses a record carrying an evidence field
// outside the subset its classification records, or any finding at all where
// the state's constructors write none: a document with evidence no
// constructor writes for that classification was not stored by one.
func refuseUnrecordedEvidence(record Record, subset evidenceSubset, verdictless bool) error {
	if verdictless {
		return refuseRecordedFinding(record)
	}

	outside := []string{}

	if record.Delta != nil && !subset.delta {
		outside = append(outside, "delta")
	}

	if record.UnknownPaths != nil && !subset.unknownPaths {
		outside = append(outside, "unknown_paths")
	}

	if record.VolatileComponents != nil && !subset.volatileComponents {
		outside = append(outside, "volatile_components")
	}

	if record.UnstableAttributes != nil && !subset.unstableAttributes {
		outside = append(outside, "unstable_attributes")
	}

	if record.Assertion != "" && !subset.assertion {
		outside = append(outside, "assertion")
	}

	if record.ClosureVerdict != "" && !subset.closureVerdict {
		outside = append(outside, "closure_verdict")
	}

	if record.DefeatedBy != "" && !subset.defeatedBy {
		outside = append(outside, "defeated_by")
	}

	if len(outside) > 0 {
		return fmt.Errorf(
			"oracle: a stored %s record names %s and %w",
			record.State, strings.Join(outside, ", "), ErrUnrecordedEvidence,
		)
	}

	return nil
}

// evidence projects the record's fields onto the context's evidence value.
func (r Record) evidence() Evidence {
	return Evidence{
		delta:              r.Delta,
		unknownPaths:       r.UnknownPaths,
		volatileComponents: r.VolatileComponents,
		unstableAttributes: r.UnstableAttributes,
		assertion:          r.Assertion,
		closureVerdict:     r.ClosureVerdict,
		defeatedBy:         r.DefeatedBy,
	}
}
