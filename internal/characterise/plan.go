package characterise

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// defaultScenario is the scenario every characterisation starts from: the
// module as it behaves with no inputs supplied.
const defaultScenario = "defaults"

// Plan produces the scaffold for a module: the provider configurations to
// mock, the scenarios to harvest, and the granularity finally in force.
//
// Nothing here executes: the plan is what the staged safety gates are judged
// against, statically, before Terraform is asked to do anything at all.
func Plan(
	configuration discovery.Configuration,
	schemas tfexec.Schemas,
	options Options,
	planned []discovery.ProviderAlias,
) Scaffold {
	scenarios, todos, values := planScenarios(configuration, options)

	scaffold := Scaffold{
		Scenarios:        scenarios,
		Todos:            todos,
		Values:           values,
		Mocks:            planMocks(configuration, schemas, planned),
		Rung:             options.Rung,
		Requested:        options.Rung,
		Escalated:        false,
		EscalationReason: "",
		Options:          options,
	}

	return escalate(scaffold, configuration)
}

// escalate applies the zero-output contract: at the outputs rung a module with
// no outputs would pin nothing and certify an empty suite, so the ladder moves
// up one and says so.
func escalate(scaffold Scaffold, configuration discovery.Configuration) Scaffold {
	if scaffold.Rung != RungOutputs || outputCount(configuration) > 0 {
		return scaffold
	}

	scaffold.Rung = RungCounts
	scaffold.Escalated = true
	scaffold.EscalationReason = "the module declares no outputs, so the outputs rung would " +
		"pin nothing: escalated to counts"

	return scaffold
}

// outputCount is the module's contract surface: the outputs it declares, in
// either syntax.
//
// `JSONExpansions` is keyed by address and carries locals as well as outputs,
// so counting it whole would let a JSON-declared local pass for an output and
// suppress the zero-output escalation the ladder depends on.
func outputCount(configuration discovery.Configuration) int {
	root, found := configuration.ModuleByDir(configuration.ModuleDir)
	if !found {
		return 0
	}

	count := len(root.Outputs)

	for address := range root.JSONExpansions {
		if strings.HasPrefix(address, "output.") {
			count++
		}
	}

	return count
}

// PlanInputs resolves the module's inputs and returns the scenarios and the
// judgement points, without planning a mock or touching a provider schema. It
// is what `tf-mut todos` reads.
func PlanInputs(
	configuration discovery.Configuration,
	options Options,
) ([]report.Scenario, []report.Todo) {
	scenarios, todos, _ := planScenarios(configuration, options)

	return scenarios, todos
}

// planScenarios builds the harvest points: the default scenario, and one more
// per conditional a variable can flip.
//
// Branch expansion is bounded and not exhaustive — one scenario per flipped
// conditional, never the cross product — because mutation testing then
// measures which residual branches stay unpinned, which is a better division
// of labour than trying to enumerate them up front.
func planScenarios(
	configuration discovery.Configuration,
	options Options,
) ([]report.Scenario, []report.Todo, map[string]map[string]string) {
	values := map[string]map[string]string{}

	root, found := configuration.ModuleByDir(configuration.ModuleDir)
	if !found {
		return nil, nil, values
	}

	inputs, todos, executable := synthesiseInputs(root, options)

	base := newScenario(root.Rel, defaultScenario, inputs, options)
	scenarios := []report.Scenario{base}
	values[base.ID] = executable

	// A scenario is only worth expanding once every input resolves: with a
	// judgement point open there is no executable scenario to vary.
	//
	// An *answered* point is resolved. `synthesiseInputs` keeps it in the slice
	// so the report can show that promotion has not been earned yet, so testing
	// the slice for emptiness would mean a module that needed one answer never
	// generated a flipped scenario again — it would characterise the default
	// branch only, and report complete having done so.
	if unresolved(todos) == 0 {
		for _, flipped := range flippedScenarios(root, inputs, options) {
			scenarios = append(scenarios, flipped.scenario)
			values[flipped.scenario.ID] = flipped.values
		}
	}

	return scenarios, todos, values
}

// newScenario names a harvest point and derives its identity.
func newScenario(
	moduleRel, name string,
	inputs []report.Input,
	options Options,
) report.Scenario {
	scenario := report.Scenario{
		ID:       "",
		Name:     name,
		StateKey: RunPrefix + name,
		File:     ScenarioFile(options.TestDirRel, name),
		Inputs:   inputs,
	}
	scenario.ID = scenarioID(moduleRel, inputs, scenario.StateKey)

	return scenario
}

// unresolved counts the judgement points still standing between the scaffold
// and an executable suite: one nobody has answered, and one whose answer did
// not survive verification.
func unresolved(todos []report.Todo) int {
	count := 0

	for _, todo := range todos {
		if todo.Status == report.TodoOpen || todo.Status == report.TodoRejected {
			count++
		}
	}

	return count
}

// synthesiseInputs resolves the module's inputs in the design's preference
// order — default, then mined validation, then typed synthesis — and records
// everything that order could not resolve as an open judgement point.
func synthesiseInputs(
	root discovery.Module,
	options Options,
) ([]report.Input, []report.Todo, map[string]string) {
	inputs := []report.Input{}
	todos := []report.Todo{}
	executable := map[string]string{}

	for _, variable := range sortedVariables(root) {
		identifier := todoID(root.Rel, variable, options.Sources)
		resolved := Synthesise(variable, options.Sources, options.Answers[identifier])

		if !resolved.Resolved() {
			todos = append(todos, todoFor(identifier, variable, resolved, options))

			continue
		}

		// An answered judgement point stays in the report as answered, not as
		// resolved: promotion is what verification earns, and it has not run.
		if resolved.Provenance == report.FromAnswer {
			answered := todoFor(identifier, variable, resolved, options)
			answered.Status = report.TodoAnswered
			answered.Diagnostic = ""
			todos = append(todos, answered)
		}

		if !resolved.Assign {
			continue
		}

		inputs = append(inputs, report.Input{
			Name:       variable.Name,
			Expression: withheld(variable, resolved.Expression),
			Provenance: resolved.Provenance,
		})
		executable[variable.Name] = resolved.Expression
	}

	return inputs, todos, executable
}

// withheld keeps a sensitive or ephemeral variable's synthesised value out of
// every artefact. Redaction applies from the first attempt onwards, not from
// the pin point: a value that never reached a plan can still be a secret.
func withheld(variable discovery.Block, expression string) string {
	for _, marker := range []string{"sensitive", "ephemeral"} {
		attribute, declared := attributeOf(variable, marker)
		if !declared {
			continue
		}

		if value, diagnostics := attribute.Expr.Value(nil); !diagnostics.HasErrors() &&
			value.Type() == cty.Bool && value.True() {
			return report.SensitiveWithheld
		}
	}

	return expression
}

// todoID is the judgement point's stable identity: the module, the variable,
// and each constraint's normalised text in declaration order.
//
// The variable alone is not enough — two constraints on one variable are
// distinct judgement points and would otherwise collapse into one — but an
// absolute path and a byte offset are too much. An identity built from those
// stops matching when the checkout moves, and again when any earlier byte in
// the declaring file shifts, so a recorded `--answer` silently misses for
// reasons that have nothing to do with the constraint it answered. What is
// left identifies the constraint by what it *says*, which is what an answer is
// an answer to; declaration order disambiguates two that say the same thing.
func todoID(moduleRel string, variable discovery.Block, sources map[string][]byte) string {
	parts := make([]string, 0, partsBeforeInputs+len(variable.Validations))
	parts = append(parts, moduleRel, variable.Name)

	for _, validation := range variable.Validations {
		parts = append(parts, normalised(sourceText(validation, sources)))
	}

	return Identify("todo-", parts...)
}

// normalised collapses a constraint's whitespace, so a reformatting that does
// not change what the constraint says does not change its identity either.
func normalised(constraint string) string {
	return strings.Join(strings.Fields(constraint), " ")
}

func todoFor(
	identifier string,
	variable discovery.Block,
	resolved Synthesis,
	options Options,
) report.Todo {
	constraintRange := resolved.ConstraintRange
	if constraintRange.Filename == "" {
		constraintRange = variable.DefRange
	}

	// A sensitive variable withholds its whole evidence bundle. The constraint
	// itself can carry the secret — `var.token == "..."` names it outright —
	// and so can a diagnostic quoting a failed attempt, so redaction applies
	// from the first attempt onwards rather than from the pin point.
	return report.Todo{
		ID: identifier, Variable: variable.Name, Status: report.TodoOpen,
		Constraint: withheld(variable, resolved.Constraint),
		Range: report.Range{
			File: variable.ModuleRel,
			Start: report.Position{
				Line: constraintRange.Start.Line, Column: constraintRange.Start.Column,
			},
			End: report.Position{
				Line: constraintRange.End.Line, Column: constraintRange.End.Column,
			},
		},
		Diagnostic: withheld(variable, resolved.Gap),
		Attempted:  redactAll(variable, resolved.Attempted),
		Artefact:   ArtefactFile(options.TestDirRel, defaultScenario),
	}
}

// redactAll withholds every attempted value of a sensitive variable: the
// mandatory case is a secret that exists only in a failed attempt.
func redactAll(variable discovery.Block, attempted []string) []string {
	redacted := make([]string, 0, len(attempted))

	for _, value := range attempted {
		redacted = append(redacted, withheld(variable, value))
	}

	return redacted
}

func sortedVariables(root discovery.Module) []discovery.Block {
	variables := slices.Clone(root.Variables)
	slices.SortFunc(variables, func(left, right discovery.Block) int {
		return strings.Compare(left.Name, right.Name)
	})

	return variables
}

func attributeOf(block discovery.Block, name string) (discovery.Attribute, bool) {
	for _, attribute := range block.Attributes {
		if attribute.Name == name {
			return attribute, true
		}
	}

	return discovery.Attribute{}, false //nolint:exhaustruct // the not-found sentinel.
}

// planMocks builds one mock per provider configuration, with a pinned default
// for every computed attribute the configuration reads downstream.
func planMocks(
	configuration discovery.Configuration,
	schemas tfexec.Schemas,
	planned []discovery.ProviderAlias,
) []Mock {
	resources, data := pinnedDefaults(configuration, schemas)

	mocks := make([]Mock, 0, len(planned))

	for _, declared := range planned {
		mocks = append(mocks, Mock{
			Name:      declared.Name,
			Alias:     declared.Alias,
			Resources: defaultsFor(resources, declared.Name),
			Data:      defaultsFor(data, declared.Name),
		})
	}

	return mocks
}

// defaultsFor selects the pinned defaults belonging to one provider.
func defaultsFor(byType map[string]map[string]string, provider string) []MockDefaults {
	types := make([]string, 0, len(byType))

	for resourceType := range byType {
		if discovery.ProviderOf(resourceType) == provider {
			types = append(types, resourceType)
		}
	}

	slices.Sort(types)

	pinned := make([]MockDefaults, 0, len(types))
	for _, resourceType := range types {
		pinned = append(pinned, MockDefaults{Type: resourceType, Defaults: byType[resourceType]})
	}

	return pinned
}

// pinnedDefaults maps each resource and data source type to the computed
// attributes that must be pinned, with their rendered literals.
func pinnedDefaults(
	configuration discovery.Configuration,
	schemas tfexec.Schemas,
) (resources, data map[string]map[string]string) {
	resources, data = map[string]map[string]string{}, map[string]map[string]string{}

	for _, reference := range configuration.ReferencedAttributes() {
		computed, known := schemas.Computed(reference.Kind, reference.Type, reference.Attribute)
		if !known || !computed {
			continue
		}

		literal, renderable := pinnedLiteral(schemas, reference)
		if !renderable {
			continue
		}

		into := resources
		if reference.Kind == dataKind {
			into = data
		}

		if into[reference.Type] == nil {
			into[reference.Type] = map[string]string{}
		}

		into[reference.Type][reference.Attribute] = literal
	}

	return resources, data
}

// dataKind is the schema lookup's name for the data source half.
const dataKind = "data"

// pinnedLiteral renders the deterministic value a mock hands back for one
// computed attribute.
//
// Only primitives are pinned. A collection's mock value is generated
// structurally, the double-run mask catches it if it varies, and rendering one
// here would need the type-correctness contract the M4 review settled cannot
// be met from a payload alone.
func pinnedLiteral(schemas tfexec.Schemas, reference discovery.AttributeRef) (string, bool) {
	declared, typed := schemas.AttributeType(reference.Kind, reference.Type, reference.Attribute)
	if !typed {
		return "", false
	}

	switch declared {
	case cty.String:
		return `"tfmut-` + reference.Type + "-" + reference.Attribute + `-0001"`, true
	case cty.Number:
		return "0", true
	case cty.Bool:
		return "false", true
	default:
		return "", false
	}
}

// expansion is one flipped scenario and the assignments its run block carries.
type expansion struct {
	scenario report.Scenario
	values   map[string]string
}

// flippedScenarios expands the branches a variable can take.
//
// In Terraform a conditional reachable from an input is overwhelmingly of the
// form `var.x == "literal"` or `var.x != null`, and the flipping value is
// sitting in the expression. One scenario per distinct flip, deduplicated,
// bounded — never the cross product.
func flippedScenarios(
	root discovery.Module,
	base []report.Input,
	options Options,
) []expansion {
	flips := branchFlips(root, base, options)
	scenarios := make([]expansion, 0, len(flips))

	for _, flip := range flips {
		inputs, values := withFlip(root, base, flip, options)
		scenarios = append(scenarios, expansion{
			scenario: newScenario(root.Rel,
				"flip_"+flip.variable+"_"+Identify("", flip.variable, flip.expression)[:flipSuffix],
				inputs, options),
			values: values,
		})
	}

	return scenarios
}

// flipSuffix is how much of the flip's content hash names its scenario: enough
// to separate the flips of one variable, short enough to read.
const flipSuffix = 6

// flip is one input assignment that takes a conditional the other way.
type flip struct {
	variable   string
	expression string
}

// withFlip overlays a flip on the default scenario's assignments, in both
// views: the redacted one the report carries and the executable one the run
// block does.
func withFlip(
	root discovery.Module,
	base []report.Input,
	flipped flip,
	options Options,
) ([]report.Input, map[string]string) {
	variable, _ := variableByName(root, flipped.variable)
	inputs := make([]report.Input, 0, len(base)+1)
	values := map[string]string{}
	replaced := false

	for _, input := range base {
		if input.Name == flipped.variable {
			inputs = append(inputs, report.Input{
				Name:       input.Name,
				Expression: withheld(variable, flipped.expression),
				Provenance: report.FromType,
			})
			values[input.Name] = flipped.expression
			replaced = true

			continue
		}

		inputs = append(inputs, input)
		values[input.Name] = executableOf(root, options, input.Name)
	}

	if !replaced {
		inputs = append(inputs, report.Input{
			Name:       flipped.variable,
			Expression: withheld(variable, flipped.expression),
			Provenance: report.FromType,
		})
		values[flipped.variable] = flipped.expression
	}

	slices.SortFunc(inputs, func(left, right report.Input) int {
		return strings.Compare(left.Name, right.Name)
	})

	return inputs, values
}

// executableOf re-resolves one variable's executable value, which is the
// redacted report value's twin.
func executableOf(root discovery.Module, options Options, name string) string {
	variable, found := variableByName(root, name)
	if !found {
		return ""
	}

	return Synthesise(variable, options.Sources,
		options.Answers[todoID(root.Rel, variable, options.Sources)]).Expression
}

func variableByName(root discovery.Module, name string) (discovery.Block, bool) {
	for _, variable := range root.Variables {
		if variable.Name == name {
			return variable, true
		}
	}

	return discovery.Block{}, false //nolint:exhaustruct // the not-found sentinel.
}

// branchFlips walks the module for conditionals a variable decides, and
// returns one flipping assignment per distinct one, in deterministic order.
func branchFlips(root discovery.Module, base []report.Input, options Options) []flip {
	byName := map[string]discovery.Block{}
	for _, variable := range root.Variables {
		byName[variable.Name] = variable
	}

	seen := map[flip]bool{}
	flips := []flip{}

	for _, body := range root.Bodies {
		discovery.WalkExpressions(body, func(expr hclsyntax.Expression) {
			for _, candidate := range flipsIn(expr, byName, base, options) {
				if seen[candidate] {
					continue
				}

				seen[candidate] = true
				flips = append(flips, candidate)
			}
		})
	}

	slices.SortFunc(flips, func(left, right flip) int {
		if order := strings.Compare(left.variable, right.variable); order != 0 {
			return order
		}

		return strings.Compare(left.expression, right.expression)
	})

	return flips
}

// flipsIn reads the flipping assignments one expression names.
func flipsIn(
	expr hclsyntax.Expression,
	variables map[string]discovery.Block,
	base []report.Input,
	options Options,
) []flip {
	operation, ok := expr.(*hclsyntax.BinaryOpExpr)
	if !ok || (operation.Op != hclsyntax.OpEqual && operation.Op != hclsyntax.OpNotEqual) {
		return nil
	}

	name, literal, found := comparedVariable(operation)
	if !found {
		return nil
	}

	variable, declared := variables[name]
	if !declared {
		return nil
	}

	value, diagnostics := literal.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() {
		return nil
	}

	expression := renderValue(value)
	if value.IsNull() {
		expression = "null"
	}

	// A candidate the base scenario already assigns is not a flip: it takes the
	// same branch under a different name, and generating it would claim to
	// have characterised the other side of a conditional nothing evaluated
	// differently. `var.env == "prod"` with a default of `"prod"` is the case
	// that caught this, and `!=` has the identical problem — the guard is
	// about the value being unchanged, not about which operator asked.
	if baseValue(variable, base) == expression {
		return nil
	}

	// The flip has to satisfy the module's own constraints like any other
	// synthesised value: a branch nobody can legally reach is not a scenario.
	if resolved := Synthesise(variable, options.Sources, expression); !resolved.Resolved() {
		return nil
	}

	return []flip{{variable: name, expression: expression}}
}

// baseValue is what the default scenario effectively assigns a variable: the
// assignment it carries where it carries one, and the variable's own declared
// default where it does not.
func baseValue(variable discovery.Block, base []report.Input) string {
	for _, input := range base {
		if input.Name == variable.Name {
			return input.Expression
		}
	}

	attribute, declared := attributeOf(variable, "default")
	if !declared {
		return ""
	}

	value, diagnostics := attribute.Expr.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() {
		return ""
	}

	if value.IsNull() {
		return "null"
	}

	return renderValue(value)
}

// comparedVariable reads the variable and the literal out of a comparison.
func comparedVariable(
	operation *hclsyntax.BinaryOpExpr,
) (name string, literal hclsyntax.Expression, found bool) {
	if name, ok := variableName(operation.LHS); ok {
		return name, operation.RHS, true
	}

	if name, ok := variableName(operation.RHS); ok {
		return name, operation.LHS, true
	}

	return "", nil, false
}

// variableName reports the variable an expression reads, when the expression
// is exactly `var.<name>` and nothing else. It is the one decoder for that
// shape: mining, flipping and answer-checking all ask the same question.
func variableName(expr hclsyntax.Expression) (string, bool) {
	traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(traversal.Traversal) != variableTraversalParts {
		return "", false
	}

	root, ok := traversal.Traversal[0].(hcl.TraverseRoot)
	if !ok || root.Name != variableRoot {
		return "", false
	}

	attribute, ok := traversal.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}

	return attribute.Name, true
}
