package characterise

import (
	"github.com/hashicorp/hcl/v2"
)

// The judgement point: the entity this context owns, its transitions, and the
// evidence promotion demands.
//
// A judgement point moves between open, answered, promoted and rejected only
// through the transitions below — never by assignment — so an unverified
// answer is unable to appear as promoted test content: promotion is reachable
// only from an answered point, and only through a call that carries the
// verification that earned it.

// TodoStatus is the closed state vocabulary of a judgement point, in the
// vocabulary the Characterisation context owns. The application layer projects
// it onto the published wire spelling at the report boundary; the strings are
// the wire spellings, and extending the set is a schema-version event, not a
// silently additive change.
type TodoStatus string

// The judgement-point states.
const (
	// TodoOpen marks a judgement point nobody has answered.
	TodoOpen TodoStatus = "open"
	// TodoAnswered marks an answer supplied and not yet verified.
	TodoAnswered TodoStatus = "answered"
	// TodoPromoted marks an answer that verified and became test content.
	TodoPromoted TodoStatus = "promoted"
	// TodoRejected marks an answer verification refuted.
	TodoRejected TodoStatus = "rejected"
)

// TodoEvidence is what a judgement point carries from the moment it opens: the
// constraint verbatim, where it is declared, and everything an answer needs.
//
// The constraint itself can carry a secret — `var.token == "..."` names it
// outright — and so can a diagnostic quoting a failed attempt, so the caller
// redacts before constructing; the evidence is stored as given.
type TodoEvidence struct {
	// ID is the judgement point's stable identity.
	ID string
	// Variable is the module input awaiting a value.
	Variable string
	// Constraint is the validation expression verbatim, or empty where the
	// variable carries none.
	Constraint string
	// ConstraintRange is where that constraint is declared. Its positions are
	// published; its file name is not — the artefact quotes the
	// module-relative file, which travels separately.
	ConstraintRange hcl.Range
	// File is the module-relative path of the declaring file.
	File string
	// Diagnostic is the last attempt's failure, redacted, empty where none.
	Diagnostic string
	// Attempted lists the values already tried, redacted, in order.
	Attempted []string
	// Artefact is the non-executable file the point is written into, relative
	// to the module directory.
	Artefact string
}

// Todo is one judgement point in one of its published states. Its fields are
// unexported: a point exists only where OpenTodo built it, and its state
// changes only through the transitions.
type Todo struct {
	id              string
	variable        string
	status          TodoStatus
	constraint      string
	constraintRange hcl.Range
	file            string
	diagnostic      string
	attempted       []string
	artefact        string
}

// OpenTodo opens a judgement point: the deterministic pipeline could not
// resolve an input, and records what an answer needs instead of guessing.
func OpenTodo(evidence TodoEvidence) Todo {
	return Todo{
		id:              evidence.ID,
		variable:        evidence.Variable,
		status:          TodoOpen,
		constraint:      evidence.Constraint,
		constraintRange: evidence.ConstraintRange,
		file:            evidence.File,
		diagnostic:      evidence.Diagnostic,
		attempted:       evidence.Attempted,
		artefact:        evidence.Artefact,
	}
}

// ID returns the judgement point's stable identity.
func (t Todo) ID() string { return t.id }

// Variable returns the module input awaiting a value.
func (t Todo) Variable() string { return t.variable }

// Status returns the point's state in the closed vocabulary.
func (t Todo) Status() TodoStatus { return t.status }

// Constraint returns the validation expression verbatim, or empty where the
// variable carries none.
func (t Todo) Constraint() string { return t.constraint }

// Range returns where the constraint is declared.
func (t Todo) Range() hcl.Range { return t.constraintRange }

// File returns the module-relative path of the declaring file.
func (t Todo) File() string { return t.file }

// Diagnostic returns the last attempt's failure, or empty where there is none.
func (t Todo) Diagnostic() string { return t.diagnostic }

// Attempted returns the values already tried, in order.
func (t Todo) Attempted() []string { return t.attempted }

// Artefact returns the non-executable file the point is written into.
func (t Todo) Artefact() string { return t.artefact }

// Answer supplies the value the reader chose for this point, moving it from
// open to answered. It records the judgement the tool refused to make; what
// the answer becomes — promoted test content or a rejected hypothesis — is
// what the verification that follows earns, and nothing else.
func (t Todo) Answer(value string) Answered {
	return Answered{
		point: Todo{
			id: t.id, variable: t.variable, status: TodoAnswered,
			constraint: t.constraint, constraintRange: t.constraintRange,
			file: t.file, attempted: t.attempted, artefact: t.artefact,
		},
		value: value,
	}
}

// Answered is a judgement point carrying a supplied answer, and the only value
// Promote and Reject accept: there is no route from open to either.
type Answered struct {
	point Todo
	// value is the answer as supplied. It is deliberately unpublished: an
	// answer may be a secret, and no report field may vary with one. The
	// executable run block carries it through the scaffold's Values, and the
	// write protocol keeps that out of every reported byte.
	value string
}

// Value returns the answer as supplied. It never reaches a report.
func (a Answered) Value() string { return a.value }

// Point returns the answered judgement point, as a report bundle carries it:
// an answer supplied, and nothing yet proven.
func (a Answered) Point() Todo { return a.point }

// Promote moves the answered point into test content. The evidence is the
// verification that earned it, and it is not optional: this transition is the
// only route to promoted, and a point nobody verified cannot take it. The
// evidence is deliberately not published — the report carries the promoted
// state and nothing else — so the parameter is referenced only to keep the
// contract spelled at the call site.
func (a Answered) Promote(evidence Verification) Todo {
	// The assignment keeps the contract's parameter referenced without
	// publishing it; nothing a report carries comes from the evidence.
	_ = evidence
	promoted := a.point
	promoted.status = TodoPromoted

	return promoted
}

// Reject moves the answered point to rejected, carrying why. A rejected point
// still needs an answer: rejection is the loop's finding, not its end.
func (a Answered) Reject(reason string) Todo {
	rejected := a.point
	rejected.status = TodoRejected
	rejected.diagnostic = reason

	return rejected
}

// Verification is the evidence promotion demands: the proof that the suite an
// answer produced was executed and passed. Its fields are unexported, and the
// engine builds it at the point its verifier succeeded, so a transition site
// names the leg that proved the point and what that leg executed.
type Verification struct {
	leg  string
	runs int
}

// Verified records the evidence of one passed verification leg.
func Verified(leg string, runs int) Verification {
	return Verification{leg: leg, runs: runs}
}

// Leg names the verification leg that proved the point.
func (v Verification) Leg() string { return v.leg }

// Runs counts the run blocks the leg executed.
func (v Verification) Runs() int { return v.runs }
