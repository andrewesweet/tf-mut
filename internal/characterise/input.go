package characterise

// The input and its provenance: where a scenario's variable assignment came
// from, in the vocabulary the Characterisation context owns.

// InputProvenance names where a scenario's variable assignment came from.
type InputProvenance string

// The synthesis preference order, in the order it is tried. These are the only
// values a provenance can carry, and the preference order is the only writer:
// every input's provenance is set by the rung that resolved it, and nothing
// else assigns one.
const (
	// FromDefault is the variable's own default.
	FromDefault InputProvenance = "default"
	// FromValidation is a value mined from a validation condition.
	FromValidation InputProvenance = "mined"
	// FromType is a value synthesised from the declared type.
	FromType InputProvenance = "typed"
	// FromAnswer is a value a judgement point's answer supplied.
	FromAnswer InputProvenance = "answered"
)

// Input is one variable assignment in a scenario. Its fields are unexported:
// an input exists only where the synthesis pipeline resolved one, which is
// what makes the preference order the only writer of its provenance.
type Input struct {
	name       string
	expression string
	provenance InputProvenance
}

// Name returns the variable the assignment is for.
func (i Input) Name() string { return i.name }

// Expression returns the assignment as it is published. A sensitive or
// ephemeral variable carries the withheld marker instead of the value; the
// executable assignment travels separately, in the scaffold's Values.
func (i Input) Expression() string { return i.expression }

// Provenance returns the rung of the preference order that produced the
// assignment.
func (i Input) Provenance() InputProvenance { return i.provenance }
