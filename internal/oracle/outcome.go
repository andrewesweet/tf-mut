// Package oracle owns the mutant outcome and every diagnosis: whether a mutant
// that every run passed produced any observable difference at all, and if so
// what the suite would have had to do to notice. An outcome exists only where
// a constructor proved the combination legal; the application layer projects
// it onto the published report at the boundary.
package oracle

// State is the aggregate verdict the oracle assigned to one mutant, in the
// vocabulary the Oracle / Verdict context owns. The application layer
// projects it onto the published wire spelling at the report boundary.
type State string

// The states the oracle assigns. A survivor carries exactly one diagnosis;
// the states below it carry none, because a diagnosis names why a mutant that
// every run passed survived and these states are not survivors. The string
// values are the closed vocabulary, not the Go spellings: the constants carry
// the State prefix so they never collide with the constructors that share
// their wire name.
const (
	// StateSurvived marks a mutant every executed run passed.
	StateSurvived State = "Survived"
	// StateStructurallyUnassertable marks a fingerprint-identical mutant of a
	// construct with no plan or state projection.
	StateStructurallyUnassertable State = "StructurallyUnassertable"
	// StateUnobservable marks a fingerprint-identical mutant of a construct
	// that projects, proven over a payload with no unknown value in the
	// mutation's forward cone.
	StateUnobservable State = "Unobservable"
)

// Diagnosis names why a survivor survived. Exactly one is assigned to every
// survivor outcome, by the precedence table in the engine's oracle.
type Diagnosis string

// The diagnoses, in precedence order — first match wins.
const (
	// IndeterminateUnknownValues marks a fingerprint-identical survivor whose
	// payload carries an unknown value, so equality cannot be proven.
	IndeterminateUnknownValues Diagnosis = "indeterminate-unknown-values"
	// IndeterminateVolatility marks a survivor whose delta remained undecidable
	// after the mutant was re-run.
	IndeterminateVolatility Diagnosis = "indeterminate-volatility"
	// WeakAssertion marks a survivor an assertion reads yet does not catch.
	WeakAssertion Diagnosis = "weak-assertion"
	// NoAssertion marks a survivor the output and local closure proves no
	// assertion reads.
	NoAssertion Diagnosis = "no-assertion"
	// Unasserted marks a survivor whose closure was defeated, so weak and
	// absent assertions cannot be honestly told apart.
	Unasserted Diagnosis = "unasserted"
)

// Outcome is one mutant's classification. Its fields are unexported: an
// Outcome exists only where a constructor proved the combination legal.
type Outcome struct {
	state     State
	diagnosis Diagnosis
	message   string
	fix       string
	evidence  Evidence
}

// State returns the aggregate verdict the oracle assigned.
func (o Outcome) State() State { return o.state }

// Diagnosis returns why a survivor survived. It is empty for an outcome that
// carries none: the terminal states are not survivors, and the constructors
// give them no way to name a diagnosis.
func (o Outcome) Diagnosis() Diagnosis { return o.diagnosis }

// Message states the finding in the reader's terms.
func (o Outcome) Message() string { return o.message }

// Fix names the change that would resolve it.
func (o Outcome) Fix() string { return o.fix }

// Evidence returns what the classification carries behind it.
func (o Outcome) Evidence() Evidence { return o.evidence }
