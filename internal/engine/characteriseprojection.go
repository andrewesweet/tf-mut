package engine

import (
	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The projection from the Characterisation context's values onto the published
// report DTOs. This is the one place the context's status and provenance
// vocabularies are spelled onto the wire, and the switch statements are
// exhaustive over the context's closed sets: a value the context gains is a
// compile-time demand on these tables. The published wire spellings are
// unchanged, and the sensitive-value withheld marker passes through untouched
// — the context publishes the marker in place of the value, and the report
// carries what it is given.
//
// projectScenarios maps the context's harvest points onto the published
// scenario DTOs, field for field: identity, naming, state key and published
// inputs are the context's arithmetic, and this is where they are spelled.
func projectScenarios(scenarios []characterise.Scenario) []report.Scenario {
	projected := make([]report.Scenario, 0, len(scenarios))

	for _, scenario := range scenarios {
		projected = append(projected, report.Scenario{
			ID:       scenario.ID(),
			Name:     scenario.Name(),
			StateKey: scenario.StateKey(),
			File:     scenario.File(),
			Inputs:   projectInputs(scenario.Inputs()),
		})
	}

	return projected
}

// projectInputs maps the context's inputs onto the published DTO. The
// expression is already the published rendering — a sensitive variable's
// withheld marker travels as the context carries it.
func projectInputs(inputs []characterise.Input) []report.Input {
	projected := make([]report.Input, 0, len(inputs))

	for _, input := range inputs {
		projected = append(projected, report.Input{
			Name:       input.Name(),
			Expression: input.Expression(),
			Provenance: projectInputProvenance(input.Provenance()),
		})
	}

	return projected
}

// projectInputProvenance translates the synthesis preference order's
// vocabulary into the published wire spelling. It is exhaustive over the
// order's closed set, so a rung the pipeline gains is a compile-time demand
// on this table.
func projectInputProvenance(provenance characterise.InputProvenance) report.InputProvenance {
	switch provenance {
	case characterise.FromDefault:
		return report.FromDefault
	case characterise.FromValidation:
		return report.FromValidation
	case characterise.FromType:
		return report.FromType
	case characterise.FromAnswer:
		return report.FromAnswer
	}

	return ""
}

// projectPins maps the context's pins onto the published DTOs, in bundle
// order.
func projectPins(pins []characterise.Pin) []report.Pin {
	projected := make([]report.Pin, 0, len(pins))

	for _, pin := range pins {
		projected = append(projected, projectPin(pin))
	}

	return projected
}

// projectPin maps one pin onto the published DTO. Only a pinned pin carries
// an expression; a skipped one carries its reason and no executable content —
// the expression the context never gave it.
func projectPin(pin characterise.Pin) report.Pin {
	projected := report.Pin{
		ID:         pin.ID(),
		Scenario:   pin.Scenario(),
		Address:    pin.Address(),
		Expression: pin.Expression(),
		Rung:       pin.Rung(),
	}

	if reason, skipped := pin.SkipReason(); skipped {
		projected.Status = projectPinSkipReason(reason)
		projected.Reason = pin.Reason()

		return projected
	}

	projected.Status = report.Pinned

	return projected
}

// projectPinSkipReason translates the Characterisation context's pin skip
// vocabulary into the published wire spelling. The switch is exhaustive over
// the context's closed set, so a reason the context gains is a compile-time
// demand on this table.
func projectPinSkipReason(reason characterise.SkipReason) report.PinStatus {
	switch reason {
	case characterise.SkipSensitive:
		return report.PinSkippedSensitive
	case characterise.SkipUnrenderable:
		return report.PinSkippedUnrenderable
	case characterise.SkipVolatile:
		return report.PinSkippedVolatile
	case characterise.SkipMockInvented:
		return report.PinSkippedMockInvented
	}

	return ""
}

// projectScaffolds maps the context's scaffolds onto the published DTOs, in
// the order the context's record carries.
func projectScaffolds(scaffolds []characterise.Scaffold) []report.Scaffold {
	projected := make([]report.Scaffold, 0, len(scaffolds))

	for _, scaffold := range scaffolds {
		projected = append(projected, projectScaffold(scaffold))
	}

	return projected
}

// projectScaffold maps one construct scaffold onto the published DTO. A
// promoted scaffold carries no artefact — the context cleared it at the
// transition — and the DTO's omitempty spelling falls out of that.
func projectScaffold(scaffold characterise.Scaffold) report.Scaffold {
	return report.Scaffold{
		ID:       scaffold.ID(),
		Kind:     scaffold.Kind(),
		Address:  scaffold.Address(),
		Status:   projectScaffoldStatus(scaffold.Status()),
		Artefact: scaffold.Artefact(),
	}
}

// projectScaffoldStatus translates the Characterisation context's scaffold
// state vocabulary into the published wire spelling. It is exhaustive over the
// context's closed set; the states are reachable only through the constructors
// and the transition, so a state the context gains is a compile-time demand on
// this table.
func projectScaffoldStatus(status characterise.ScaffoldStatus) report.ScaffoldStatus {
	switch status {
	case characterise.StatusScaffolded:
		return report.Scaffolded
	case characterise.StatusPromoted:
		return report.ScaffoldPromoted
	}

	return ""
}

// projectTodos maps the context's judgement points onto the published DTOs,
// in bundle order.
func projectTodos(todos []characterise.Todo) []report.Todo {
	projected := make([]report.Todo, 0, len(todos))

	for _, todo := range todos {
		projected = append(projected, projectTodo(todo))
	}

	return projected
}

// projectTodo maps one judgement point onto the published DTO. The range is
// the declared constraint's positions over the module-relative file, exactly
// as the artefact quotes them.
func projectTodo(todo characterise.Todo) report.Todo {
	constraintRange := todo.Range()

	return report.Todo{
		ID:         todo.ID(),
		Variable:   todo.Variable(),
		Status:     projectTodoStatus(todo.Status()),
		Constraint: todo.Constraint(),
		Range: report.Range{
			File: todo.File(),
			Start: report.Position{
				Line: constraintRange.Start.Line, Column: constraintRange.Start.Column,
			},
			End: report.Position{
				Line: constraintRange.End.Line, Column: constraintRange.End.Column,
			},
		},
		Diagnostic: todo.Diagnostic(),
		Attempted:  todo.Attempted(),
		Artefact:   todo.Artefact(),
	}
}

// projectTodoStatus translates the judgement point's state vocabulary into
// the published wire spelling. It is exhaustive over the context's closed
// set; the states are reachable only through the transitions, so a state the
// context gains is a compile-time demand on this table.
func projectTodoStatus(status characterise.TodoStatus) report.TodoStatus {
	switch status {
	case characterise.TodoOpen:
		return report.TodoOpen
	case characterise.TodoAnswered:
		return report.TodoAnswered
	case characterise.TodoPromoted:
		return report.TodoPromoted
	case characterise.TodoRejected:
		return report.TodoRejected
	}

	return ""
}
