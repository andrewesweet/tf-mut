package characterise

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// Harvest is everything one double run observed, in the form the pinning stage
// consumes it.
type Harvest struct {
	// Payloads is the canonical projection of the first run.
	Payloads []fingerprint.Payload
	// Mask is the volatile set the double run derived: every path in it varies
	// between two runs of the same configuration, so pinning it would generate
	// a test that is flaky by construction.
	Mask fingerprint.Mask
}

// The canonical payload prefixes each rung reads.
const (
	outputPrefix   = "outputs."
	resourcePrefix = "root_module.resources["
	valuesSegment  = "].values."
)

// Pin turns a harvest into the pins of the scaffold's granularity.
//
// Every admitted value goes through the M4 rendering, addressing and
// sensitivity adapters unchanged: the assertion this writes and the assertion
// `suggest` writes are produced by one contract, so a value that is
// unrenderable for one is unrenderable for both.
func Pin(
	scaffold Scaffold,
	configuration discovery.Configuration,
	schemas tfexec.Schemas,
	harvest Harvest,
) []report.Pin {
	masked := map[string]bool{}
	for _, path := range harvest.Mask.Paths() {
		masked[path] = true
	}

	pins := []report.Pin{}
	seen := map[string]bool{}

	for _, payload := range harvest.Payloads {
		if payload.Kind != tfexec.PayloadState {
			continue
		}

		scenario, found := scenarioOf(scaffold, payload.Run)
		if !found {
			continue
		}

		pins = append(pins, valuePins(scaffold, schemas, payload, scenario, masked, seen)...)
		pins = append(pins, countPins(scaffold, configuration, payload, scenario, seen)...)
	}

	slices.SortFunc(pins, func(left, right report.Pin) int {
		if order := strings.Compare(left.Scenario, right.Scenario); order != 0 {
			return order
		}

		return strings.Compare(left.Address, right.Address)
	})

	return pins
}

// scenarioOf maps a run block name back to the scenario that generated it.
func scenarioOf(scaffold Scaffold, run string) (report.Scenario, bool) {
	for _, scenario := range scaffold.Scenarios {
		if RunPrefix+scenario.Name == run {
			return scenario, true
		}
	}

	return report.Scenario{}, false //nolint:exhaustruct // the not-found sentinel.
}

// valuePins pins the output and configured-attribute values of one payload.
func valuePins(
	scaffold Scaffold,
	schemas tfexec.Schemas,
	payload fingerprint.Payload,
	scenario report.Scenario,
	masked, seen map[string]bool,
) []report.Pin {
	sensitiveValues := payload.SensitiveRenderings()

	paths := make([]string, 0, len(payload.Values))
	for path := range payload.Values {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	pins := []report.Pin{}

	for _, path := range paths {
		rung, admitted := rungOf(path)
		if !admitted || !scaffold.Rung.Includes(rung) {
			continue
		}

		address, attribute, ok := fingerprint.Split(path)
		if !ok {
			continue
		}

		key := scenario.ID + "\x00" + expressionAddress(address, attribute)
		if seen[key] {
			continue
		}

		seen[key] = true

		pins = append(pins, onePin(pinContext{
			scaffold: scaffold, schemas: schemas, payload: payload, scenario: scenario,
			rung: rung, path: path, address: address, attribute: attribute,
			masked: masked[path], sensitiveValues: sensitiveValues,
		}))
	}

	return pins
}

// pinContext is one candidate value and everything the decision needs.
type pinContext struct {
	scaffold        Scaffold
	schemas         tfexec.Schemas
	payload         fingerprint.Payload
	scenario        report.Scenario
	rung            Rung
	path            string
	address         string
	attribute       string
	masked          bool
	sensitiveValues map[string]bool
}

// onePin decides one candidate value's fate. The order is the contract: a
// value the mask removed was never observed honestly, a sensitive value must
// not reach a renderer at all, and only what survives both is expressed.
func onePin(context pinContext) report.Pin {
	value := context.payload.Values[context.path]
	expression := expressionAddress(context.address, context.attribute)

	skip := func(status report.PinStatus, reason string) report.Pin {
		return report.Pin{
			ID: pinID(context.scenario.ID, expression, ""), Scenario: context.scenario.ID,
			Address: expression, Expression: "", Status: status, Reason: reason,
			Rung: string(context.rung),
		}
	}

	if context.masked {
		return skip(report.PinSkippedVolatile,
			"the double run proved this value varies between two runs of the same configuration")
	}

	sensitive := context.payload.Sensitive(context.path) || context.sensitiveValues[value]
	if sensitive {
		return skip(report.PinSkippedSensitive,
			"Terraform marks this value, or a container of it, sensitive")
	}

	if reason, invented := mockInvented(context); invented {
		return skip(report.PinSkippedMockInvented, reason)
	}

	rendered, err := suggest.Express(
		discovery.RunBlock{Name: RunPrefix + context.scenario.Name}, //nolint:exhaustruct // the adapter reads the address.
		context.schemas,
		report.Change{
			Run: RunPrefix + context.scenario.Name, Path: context.path,
			Address: expression, Baseline: value, Mutant: "", Sensitive: false,
		},
	)
	if err != nil {
		if errors.Is(err, suggest.ErrSensitive) {
			return skip(report.PinSkippedSensitive, "the sensitivity predicate refused this value")
		}

		return skip(report.PinSkippedUnrenderable, err.Error())
	}

	return report.Pin{
		ID: pinID(context.scenario.ID, expression, rendered), Scenario: context.scenario.ID,
		Address: expression, Expression: rendered, Status: report.Pinned, Reason: "",
		Rung: string(context.rung),
	}
}

// mockInvented reports a value the provider computes rather than the
// configuration determines. The configured rung never pins one: its value came
// from the mock, and pinning it would characterise the mock.
func mockInvented(context pinContext) (string, bool) {
	if context.rung != RungConfigured {
		return "", false
	}

	kind, resourceType, ok := schemaCoordinates(context.address)
	if !ok {
		return "", false
	}

	name, _, _ := strings.Cut(context.attribute, ".")

	computed, known := context.schemas.Computed(kind, resourceType, name)
	if !known {
		return fmt.Sprintf("no provider schema describes %s.%s, so it cannot be told apart "+
			"from a value the mock invented", resourceType, name), true
	}

	if computed {
		return fmt.Sprintf("the provider computes %s.%s, so its value came from the mock "+
			"rather than from the configuration", resourceType, name), true
	}

	return "", false
}

// schemaCoordinates maps a resource address onto the schema lookup's key.
func schemaCoordinates(address string) (kind, resourceType string, ok bool) {
	trimmed := discovery.ParseAddr(address)
	if len(trimmed.Parts) == 0 {
		return "", "", false
	}

	parts := trimmed.Parts
	if parts[0] == dataKind {
		if len(parts) < addressWithKind {
			return "", "", false
		}

		return dataKind, parts[1], true
	}

	return "resource", parts[0], true
}

const addressWithKind = 2

// rungOf reports which ladder level a canonical payload path belongs to.
func rungOf(path string) (Rung, bool) {
	switch {
	case strings.HasPrefix(path, outputPrefix):
		return RungOutputs, true
	case strings.HasPrefix(path, resourcePrefix) && strings.Contains(path, valuesSegment):
		return RungConfigured, true
	default:
		return "", false
	}
}

// expressionAddress rejoins an address and attribute path the way the
// assertion expression spells it.
func expressionAddress(address, attribute string) string {
	if attribute == "" {
		return address
	}

	return address + "." + attribute
}

// countPins pins the instance count of every resource collection the module
// declares with count or for_each.
//
// `length` is type-correct over every collection, which is why it is one of
// the three forms the M4 rendering contract admits; the number itself is
// rendered by the same value machinery.
func countPins(
	scaffold Scaffold,
	configuration discovery.Configuration,
	payload fingerprint.Payload,
	scenario report.Scenario,
	seen map[string]bool,
) []report.Pin {
	if !scaffold.Rung.Includes(RungCounts) {
		return nil
	}

	counts := instanceCounts(payload)
	pins := []report.Pin{}

	for _, module := range configuration.Modules {
		if module.Dir != configuration.ModuleDir {
			continue
		}

		for _, block := range module.Resources {
			if !block.HasCount && !block.HasForEach {
				continue
			}

			key := scenario.ID + "\x00length(" + block.Address + ")"
			if seen[key] {
				continue
			}

			seen[key] = true

			pins = append(pins, report.Pin{
				ID:       pinID(scenario.ID, "length("+block.Address+")", strconv.Itoa(counts[block.Address])),
				Scenario: scenario.ID, Address: "length(" + block.Address + ")",
				Expression: "length(" + block.Address + ") == " + strconv.Itoa(counts[block.Address]),
				Status:     report.Pinned, Reason: "", Rung: string(RungCounts),
			})
		}
	}

	return pins
}

// instanceCounts counts the instances of each resource collection in a state
// payload.
func instanceCounts(payload fingerprint.Payload) map[string]int {
	instances := map[string]map[string]bool{}

	for path := range payload.Values {
		rest, found := strings.CutPrefix(path, resourcePrefix)
		if !found {
			continue
		}

		instance, _, closed := strings.Cut(rest, "]")
		if !closed {
			continue
		}

		collection := discovery.ParseAddr(instance).String()
		if instances[collection] == nil {
			instances[collection] = map[string]bool{}
		}

		instances[collection][instance] = true
	}

	counts := map[string]int{}
	for collection, members := range instances {
		counts[collection] = len(members)
	}

	return counts
}
