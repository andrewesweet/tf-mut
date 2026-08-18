package characterise

import (
	"slices"
	"strings"

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
) Scaffold {
	scenarios, todos := planScenarios(configuration, options)

	scaffold := Scaffold{
		Scenarios:        scenarios,
		Todos:            todos,
		Mocks:            planMocks(configuration, schemas),
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

func outputCount(configuration discovery.Configuration) int {
	root, found := configuration.ModuleByDir(configuration.ModuleDir)
	if !found {
		return 0
	}

	return len(root.Outputs) + len(root.JSONExpansions)
}

// planScenarios builds the harvest points.
//
// The milestone's tracer bullet is the default scenario: every input at its
// declared default. An input with no default has no deterministic value, and
// inventing one is the judgement the tool never makes — it becomes a TODO.
func planScenarios(
	configuration discovery.Configuration,
	options Options,
) ([]report.Scenario, []report.Todo) {
	root, found := configuration.ModuleByDir(configuration.ModuleDir)
	if !found {
		return nil, nil
	}

	inputs, todos := synthesiseInputs(root, options)

	scenario := report.Scenario{
		ID:       "",
		Name:     defaultScenario,
		StateKey: RunPrefix + defaultScenario,
		File:     ScenarioFile(options.TestDirRel, defaultScenario),
		Inputs:   inputs,
	}
	scenario.ID = scenarioID(root.Rel, inputs, scenario.StateKey)

	return []report.Scenario{scenario}, todos
}

// synthesiseInputs resolves the module's inputs in the design's preference
// order. The milestone implements the first rung — the declared default — and
// records everything else as an open judgement point.
func synthesiseInputs(root discovery.Module, options Options) ([]report.Input, []report.Todo) {
	_ = options

	inputs := []report.Input{}
	todos := []report.Todo{}

	variables := slices.Clone(root.Variables)
	slices.SortFunc(variables, func(left, right discovery.Block) int {
		return strings.Compare(left.Name, right.Name)
	})

	for _, variable := range variables {
		if _, declared := attributeOf(variable, "default"); declared {
			// A declared default needs no assignment at all: leaving it out is
			// what characterising "the module as it behaves with no inputs"
			// means, and it keeps the generated run block free of values the
			// module already states.
			continue
		}

		todos = append(todos, report.Todo{
			ID: identify("todo-", root.Rel, variable.Name), Variable: variable.Name,
			Status: report.TodoOpen, Constraint: "",
			Range: report.Range{
				File:  variable.ModuleRel,
				Start: report.Position{Line: variable.DefRange.Start.Line, Column: variable.DefRange.Start.Column},
				End:   report.Position{Line: variable.DefRange.End.Line, Column: variable.DefRange.End.Column},
			},
			Diagnostic: "the variable declares no default and this version synthesises no value for it",
			Attempted:  nil, Artefact: "",
		})
	}

	return inputs, todos
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
func planMocks(configuration discovery.Configuration, schemas tfexec.Schemas) []Mock {
	resources, data := pinnedDefaults(configuration, schemas)

	configurations := Configurations(configuration)
	mocks := make([]Mock, 0, len(configurations))

	for _, declared := range configurations {
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
