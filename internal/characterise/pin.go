package characterise

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The pin lifecycle.
//
// A pin is one harvested value at the chosen granularity, and it exists in
// exactly one of two states: pinned, carrying the assert condition the value
// was expressed as, or skipped, carrying why the pinning stage refused it and
// nothing else. The rule "only pinned carries an expression; every skipped
// status carries a reason and no executable content" is the constructors'
// signatures: Pinned is the only spelling that takes an expression, and
// PinSkipped takes none, so a skipped pin cannot smuggle executable content
// into a generated suite. The identity the published row requires — the
// scenario, the address and the rung — travels through both constructors,
// because the report schema marks those required for every status.
//
// SkipReason is why the pinning stage refused a value. It is the
// Characterisation context's own closed vocabulary, not a publication
// status; the application layer projects it onto the published wire
// spelling. Where a pin's fate turns on an observable difference, that
// difference is the Oracle context's delta change type — the same
// `fingerprint.Change` the survivor evidence carries — never a publication
// DTO.

// SkipReason is why a pin was skipped rather than pinned.
type SkipReason string

// The complete skip vocabulary. Nothing else may be assigned.
const (
	// SkipSensitive marks a value Terraform marks sensitive. Neither the
	// value nor any rendering of it reaches an artefact.
	SkipSensitive SkipReason = "skipped-sensitive"
	// SkipUnrenderable marks a value no type-correct Terraform equality
	// expresses, as the M4 rendering contract decides it.
	SkipUnrenderable SkipReason = "skipped-unrenderable"
	// SkipVolatile marks a value the double run proved varies between two
	// runs of the same configuration.
	SkipVolatile SkipReason = "skipped-volatile"
	// SkipMockInvented marks a schema-computed value the mock invented rather
	// than the configuration determined.
	SkipMockInvented SkipReason = "skipped-mock-invented"
)

// outcome is the state a pin carries. The zero value is no state at all, and
// only the constructors assign one.
type outcome uint8

const (
	outcomePinned outcome = iota + 1
	outcomeSkipped
)

// Pin is one harvested value at the chosen granularity. Its fields are
// unexported: a pin exists only where Pinned or PinSkipped built it, and
// every value carries exactly what its state requires and nothing a skipped
// one must not.
type Pin struct {
	id         string
	scenario   string
	address    string
	rung       string
	outcome    outcome
	skip       SkipReason
	expression string
	reason     string
}

// Pinned pins a harvested value as the assert condition that expresses it.
// The identifier is the stable content hash over the scenario, the address
// and the expression, so it survives a re-run.
func Pinned(scenario, address, expression, rung string) Pin {
	return Pin{
		id:         PinID(scenario, address, expression),
		scenario:   scenario,
		address:    address,
		rung:       rung,
		outcome:    outcomePinned,
		expression: expression,
	}
}

// PinSkipped records the pinning stage's refusal of a value. It takes no
// expression parameter — no reason in the closed vocabulary permits one — so
// a refused value is reported as a refusal and can never be dressed as an
// assertion. It does take the identity the published row requires (scenario,
// address and rung), which the report schema marks required for every
// status; a reason-and-detail-only constructor could not spell a
// wire-complete row. The identifier hashes an empty expression, so a skipped
// pin's identity is stable across re-runs exactly as a pinned one's is.
func PinSkipped(scenario, address, rung string, reason SkipReason, detail string) Pin {
	return Pin{
		id:       PinID(scenario, address, ""),
		scenario: scenario,
		address:  address,
		rung:     rung,
		outcome:  outcomeSkipped,
		skip:     reason,
		reason:   detail,
	}
}

// ID is the stable content identifier.
func (p Pin) ID() string { return p.id }

// Scenario is the identifier of the scenario the value was harvested from.
func (p Pin) Scenario() string { return p.scenario }

// Address is the Terraform address the pin is about.
func (p Pin) Address() string { return p.address }

// Rung is the ladder level the pin belongs to.
func (p Pin) Rung() string { return p.rung }

// Expression is the generated assert condition, for a pinned pin. A skipped
// one carries none.
func (p Pin) Expression() string { return p.expression }

// Reason states why a skipped pin was skipped. Empty when pinned.
func (p Pin) Reason() string { return p.reason }

// IsPinned reports whether the pin expresses a value. A pin that is neither
// pinned nor skipped cannot be constructed.
func (p Pin) IsPinned() bool { return p.outcome == outcomePinned }

// SkipReason is the generation-time refusal, and whether this pin is one.
func (p Pin) SkipReason() (SkipReason, bool) { return p.skip, p.outcome == outcomeSkipped }

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

// PinHarvest turns a harvest into the pins of the scaffold's granularity.
//
// Every admitted value goes through the M4 rendering, addressing and
// sensitivity adapters unchanged: the assertion this writes and the assertion
// `suggest` writes are produced by one contract, so a value that is
// unrenderable for one is unrenderable for both.
func PinHarvest(
	scaffold Scaffold,
	configuration discovery.Configuration,
	schemas tfexec.Schemas,
	harvest Harvest,
) []Pin {
	masked := map[string]bool{}
	for _, path := range harvest.Mask.Paths() {
		masked[path] = true
	}

	pins := []Pin{}
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

	slices.SortFunc(pins, func(left, right Pin) int {
		if order := strings.Compare(left.Scenario(), right.Scenario()); order != 0 {
			return order
		}

		return strings.Compare(left.Address(), right.Address())
	})

	return pins
}

// scenarioOf maps a run block name back to the scenario that generated it.
func scenarioOf(scaffold Scaffold, run string) (ScenarioPlan, bool) {
	for _, scenario := range scaffold.Scenarios {
		if RunPrefix+scenario.Name == run {
			return scenario, true
		}
	}

	return ScenarioPlan{}, false //nolint:exhaustruct // the not-found sentinel.
}

// valuePins pins the output and configured-attribute values of one payload.
func valuePins(
	scaffold Scaffold,
	schemas tfexec.Schemas,
	payload fingerprint.Payload,
	scenario ScenarioPlan,
	masked, seen map[string]bool,
) []Pin {
	sensitiveValues := payload.SensitiveRenderings()

	paths := make([]string, 0, len(payload.Values))
	for path := range payload.Values {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	pins := []Pin{}

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
	scenario        ScenarioPlan
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
func onePin(context pinContext) Pin {
	value := context.payload.Values[context.path]
	expression := expressionAddress(context.address, context.attribute)

	skip := func(reason SkipReason, detail string) Pin {
		return PinSkipped(context.scenario.ID, expression, string(context.rung), reason, detail)
	}

	if context.masked {
		return skip(SkipVolatile,
			"the double run proved this value varies between two runs of the same configuration")
	}

	sensitive := context.payload.Sensitive(context.path) || context.sensitiveValues[value]
	if sensitive {
		return skip(SkipSensitive,
			"Terraform marks this value, or a container of it, sensitive")
	}

	if reason, invented := mockInvented(context); invented {
		return skip(SkipMockInvented, reason)
	}

	rendered, err := suggest.Express(
		discovery.RunBlock{Name: RunPrefix + context.scenario.Name}, //nolint:exhaustruct // the adapter reads the address.
		context.schemas,
		fingerprint.Change{
			Run: RunPrefix + context.scenario.Name, Path: context.path,
			Address: expression, Baseline: value, Mutant: "", Sensitive: false,
		},
	)
	if err != nil {
		if errors.Is(err, suggest.ErrSensitive) {
			return skip(SkipSensitive, "the sensitivity predicate refused this value")
		}

		return skip(SkipUnrenderable, err.Error())
	}

	return Pinned(context.scenario.ID, expression, rendered, string(context.rung))
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
		if len(parts) < dataAddressParts {
			return "", "", false
		}

		return dataKind, parts[1], true
	}

	return "resource", parts[0], true
}

// dataAddressParts is the length of `data.<type>`: the shortest address that
// names a data source's type.
const dataAddressParts = 2

// rungOf reports which ladder level a canonical payload path belongs to.
//
// Its twin in the engine, `rungOfExpression`, classifies a *generated
// expression* rather than a payload path, because that is all a suggestion
// carries. The two read different inputs and neither can be derived from the
// other; what they must agree on is the ladder itself, which is the exported
// `Rung` vocabulary they both return.
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
	scenario ScenarioPlan,
	seen map[string]bool,
) []Pin {
	if !scaffold.Rung.Includes(RungCounts) {
		return nil
	}

	instances := instancesOf(payload)
	pins := []Pin{}

	for _, module := range configuration.Modules {
		if module.Dir != configuration.ModuleDir {
			continue
		}

		for _, block := range module.Resources {
			if !block.HasCount && !block.HasForEach {
				continue
			}

			pins = append(pins, countPin(scenario, block.Address,
				len(instances[block.Address]), seen)...)

			// A for_each collection's identity is its key set, not its size:
			// moving from {a} to {b} preserves the count and changes exactly
			// the thing the rung is named for. `keys` returns them sorted, so
			// the comparison is a list equality and type-correct.
			if block.HasForEach {
				pins = append(pins, keyPin(scenario, block.Address,
					instanceKeys(instances[block.Address]), seen)...)
			}
		}
	}

	return pins
}

// countPin pins one resource collection's instance count.
func countPin(scenario ScenarioPlan, address string, count int, seen map[string]bool) []Pin {
	expression := "length(" + address + ") == " + strconv.Itoa(count)

	return onlyOnce(scenario, "length("+address+")", expression, seen)
}

// keyPin pins one for_each collection's key set.
func keyPin(
	scenario ScenarioPlan,
	address string,
	keys []string,
	seen map[string]bool,
) []Pin {
	// Rendered through the same value machinery every other literal goes
	// through. A key is arbitrary text — `for_each` over a map accepts a
	// quote, a backslash and a `${` alike — and re-quoting it by concatenation
	// produces HCL that either does not parse or interpolates.
	rendered := make([]string, 0, len(keys))
	for _, key := range keys {
		rendered = append(rendered, renderValue(cty.StringVal(key)))
	}

	expression := "keys(" + address + ") == [" + strings.Join(rendered, ", ") + "]"

	return onlyOnce(scenario, "keys("+address+")", expression, seen)
}

// onlyOnce emits a counts-rung pin the first time its address is seen.
func onlyOnce(
	scenario ScenarioPlan,
	address, expression string,
	seen map[string]bool,
) []Pin {
	key := scenario.ID + "\x00" + address
	if seen[key] {
		return nil
	}

	seen[key] = true

	return []Pin{Pinned(scenario.ID, address, expression, string(RungCounts))}
}

// instancesOf groups a state payload's resource instances by the collection
// they belong to.
func instancesOf(payload fingerprint.Payload) map[string]map[string]bool {
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

	return instances
}

// unescapeTemplate reverses HCL's template escapes, which an instance address
// carries and Go's own unquoting knows nothing about.
//
// An address is quoted the way HCL quotes a string, so a key containing `${`
// arrives spelled `$${`. `strconv.Unquote` handles the backslash escapes the
// two languages share and leaves this one in place, so the key came back one
// escape too long and the renderer — correctly — escaped it again, pinning
// `"dollar$$${brace"` for a key that is `dollar${brace`. The suite then failed
// its own harvest, reported as a generator defect with nothing naming the key.
func unescapeTemplate(key string) string {
	return strings.NewReplacer("$${", "${", "%%{", "%{").Replace(key)
}

// instanceKeys reads the for_each keys out of a collection's instance
// addresses, sorted the way `keys` returns them.
func instanceKeys(members map[string]bool) []string {
	keys := make([]string, 0, len(members))

	for instance := range members {
		_, bracketed, found := strings.Cut(instance, "[")
		if !found {
			continue
		}

		key := strings.TrimSuffix(bracketed, "]")
		if unquoted, err := strconv.Unquote(key); err == nil {
			key = unquoted
		}

		keys = append(keys, unescapeTemplate(key))
	}

	slices.Sort(keys)

	return keys
}
