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
// The scenario projection is the interim half: the scenario lifecycle itself
// moves into the context with #108, and until then the planned scenario
// (identity, naming, state key, published inputs) is what the engine projects.

// projectScenarios maps the planned harvest points onto the published
// scenario DTOs, field for field.
func projectScenarios(plans []characterise.ScenarioPlan) []report.Scenario {
	scenarios := make([]report.Scenario, 0, len(plans))

	for _, plan := range plans {
		scenarios = append(scenarios, report.Scenario{
			ID:       plan.ID,
			Name:     plan.Name,
			StateKey: plan.StateKey,
			File:     plan.File,
			Inputs:   projectInputs(plan.Inputs),
		})
	}

	return scenarios
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
