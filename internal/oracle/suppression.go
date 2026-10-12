package oracle

// Suppression is the reasoned directive or configured exclusion an Ignored
// outcome was decided from. The engine constructs it from the policy's
// decision and the projection writes it onto the published mutant field for
// field, so what the mutant was suppressed by flows through the outcome that
// records the suppression.
type Suppression struct {
	// Kind is comment, config-operator, config-path or config-resource.
	Kind string `json:"kind"`
	// Operators lists the operator identifiers the directive named.
	Operators []string `json:"operators"`
	// Reason is the mandatory justification; empty on a rejected directive.
	Reason string `json:"reason"`
	// Accepted reports whether the directive suppressed anything.
	Accepted bool `json:"accepted"`
	// Range locates the directive, where it is an inline comment.
	Range *Range `json:"range"`
	// Mutants lists the identifiers the suppression applied to.
	Mutants []string `json:"mutants"`
	// Rejection explains why a directive did not suppress.
	Rejection string `json:"rejection"`
}
