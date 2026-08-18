// Package characterise scaffolds a characterisation suite for a module that
// has none.
//
// Every stage is deterministic and no stage involves a language model: the
// mocks come from the provider schemas, the scenarios from the module's own
// inputs, and the assertions from what Terraform reported when the
// assertion-less scaffold ran. The package plans and renders; executing the
// plan, judging it against the safety gates and writing it are the engine's.
package characterise

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// Rung is a level of the pinning granularity ladder.
type Rung string

// The ladder, cheapest and least brittle first.
const (
	// RungOutputs pins every output value: the module's contract surface.
	RungOutputs Rung = "outputs"
	// RungCounts adds instance counts per resource collection.
	RungCounts Rung = "counts"
	// RungConfigured adds every configured — non-computed — attribute the
	// payload records. Full characterisation, brittle by design.
	RungConfigured Rung = "configured"
)

// ErrRung reports a granularity that is not on the ladder.
var ErrRung = errors.New("unknown pinning granularity")

// ParseRung validates a requested granularity.
func ParseRung(name string) (Rung, error) {
	switch Rung(name) {
	case RungOutputs, RungCounts, RungConfigured:
		return Rung(name), nil
	case "":
		return RungOutputs, nil
	default:
		return "", fmt.Errorf("%w: %s (want outputs, counts or configured)", ErrRung, name)
	}
}

// ladder orders the rungs, cheapest first. A rung pins everything the rungs
// below it pin.
//
//nolint:gochecknoglobals // an immutable lookup table.
var ladder = map[Rung]int{
	RungOutputs:    levelOutputs,
	RungCounts:     levelCounts,
	RungConfigured: levelConfigured,
}

// The ladder's levels, in order.
const (
	levelOutputs = iota
	levelCounts
	levelConfigured
)

// includes reports whether a rung pins everything a lower rung pins.
func (r Rung) includes(other Rung) bool {
	return ladder[r] >= ladder[other]
}

// The generated file's naming contract: one file per scenario, under the test
// directory, named from the scenario. Deterministic, so a second invocation
// over an unchanged module targets exactly the same paths.
const (
	filePrefix = "characterise_"
	fileSuffix = ".tftest.hcl"
	// ArtefactSuffix is the non-executable artefact class's extension. It is
	// deliberately not one `terraform test` reads, so nothing unverified can
	// ever execute: the executable suite is green by construction and the
	// artefact is the editable surface.
	ArtefactSuffix = ".tfmut-todo.hcl"
	// RunPrefix names the generated run blocks.
	RunPrefix = "characterise_"
)

// ScenarioFile is the module-relative path of a scenario's generated file.
func ScenarioFile(testDirRel, scenario string) string {
	return path.Join(testDirRel, filePrefix+scenario+fileSuffix)
}

// ArtefactFile is the module-relative path of a scenario's non-executable
// artefact.
func ArtefactFile(testDirRel, scenario string) string {
	return path.Join(testDirRel, filePrefix+scenario+ArtefactSuffix)
}

// Options are the caller's choices for one characterisation.
type Options struct {
	// Rung is the requested granularity.
	Rung Rung
	// TestDirRel is the test directory relative to the module.
	TestDirRel string
	// Version is the tool version recorded in each generated file's header.
	Version string
	// Sources are the module files as discovery read them, keyed by absolute
	// path, so a constraint can be quoted into a TODO verbatim.
	Sources map[string][]byte
	// Answers maps a TODO identifier to the value supplied for it, from the
	// edited artefact or from --answer.
	Answers map[string]string
}

// Scaffold is the planned suite: what would be written, and the entities the
// report describes it with.
type Scaffold struct {
	// Scenarios are the harvest points, in deterministic order.
	Scenarios []report.Scenario
	// Todos are the judgement points the deterministic pipeline could not
	// resolve. A scenario with an open TODO is not executable, so the scaffold
	// travels as a non-executable artefact until one is answered.
	Todos []report.Todo
	// Mocks are the provider configurations the scaffold plans a mock for.
	// The staged provider gate is evaluated against exactly this list.
	Mocks []Mock
	// Rung is the granularity finally in force.
	Rung Rung
	// Requested is the granularity the caller asked for.
	Requested Rung
	// Escalated reports the zero-output auto-escalation.
	Escalated bool
	// EscalationReason states why, in the reader's terms.
	EscalationReason string
	// Options are the choices the scaffold was planned under.
	Options Options
}

// Mock is one planned `mock_provider` block: a provider configuration and the
// pinned defaults it gives the resource and data source types in use.
type Mock struct {
	// Name is the provider local name.
	Name string
	// Alias is the configuration alias, empty for the default configuration.
	Alias string
	// Resources are the pinned `mock_resource` bodies, ordered by type.
	Resources []MockDefaults
	// Data are the pinned `mock_data` bodies, ordered by type.
	Data []MockDefaults
}

// MockDefaults pins the computed attributes of one resource or data source
// type.
//
// Pinning is not cosmetic: auto-generated mock values are non-deterministic
// across runs, so an unpinned computed attribute that reached an assertion
// would make the generated suite flaky by construction.
type MockDefaults struct {
	Type string
	// Defaults maps attribute name to its rendered Terraform literal, ordered
	// by name when rendered.
	Defaults map[string]string
}

// identify derives a stable, content-derived identifier.
func identify(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return prefix + hex.EncodeToString(sum[:])[:identifierLength]
}

// variableRoot is the traversal root every module input is read through.
const variableRoot = "var"

const (
	identifierLength = 12
	// partsBeforeInputs is the module and the state key.
	partsBeforeInputs = 2
)

// Digest is the content digest the write protocol and the provenance registry
// commit against.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)

	return hex.EncodeToString(sum[:])
}

// scenarioID is a hash over the module, the input assignment set and the
// state key: two scenarios with the same inputs in the same module are the
// same scenario, whatever they are named.
func scenarioID(moduleRel string, inputs []report.Input, stateKey string) string {
	parts := make([]string, 0, len(inputs)+partsBeforeInputs)
	parts = append(parts, moduleRel, stateKey)

	for _, input := range inputs {
		parts = append(parts, input.Name+"="+input.Expression)
	}

	return identify("scn-", parts...)
}

// pinID is a hash over the scenario, the address and the expression.
func pinID(scenario, address, expression string) string {
	return identify("pin-", scenario, address, expression)
}

// GeneratedHeader is the comment every generated file carries.
//
// It states the regression semantics in the file itself, because a
// characterisation suite pins today's behaviour including today's bugs — the
// point of the technique, and a thing a reader who finds the file in six
// months has no other way to learn.
func GeneratedHeader(version, scenario string) string {
	return strings.Join([]string{
		"# Generated by tf-mut characterise " + version + ".",
		"#",
		"# This suite pins what the module does today, including any bug it does",
		"# today. It detects change; it does not claim the pinned behaviour is",
		"# correct. Deciding which changes are fixes is yours.",
		"#",
		"# Scenario: " + scenario,
		"",
	}, "\n")
}

// Configurations lists every provider configuration the closure declares, in
// deterministic order: the default configuration of each required provider,
// then each declared alias.
//
// This is the list the staged provider gate is evaluated against. One mock per
// provider *requirement* is not enough — Terraform matches mocks to
// configurations by alias, so an aliased configuration without its own mock
// reaches a real provider.
func Configurations(configuration discovery.Configuration) []discovery.ProviderAlias {
	seen := map[discovery.ProviderAlias]bool{}
	ordered := []discovery.ProviderAlias{}

	add := func(alias discovery.ProviderAlias) {
		if alias.Name == discovery.BuiltinProvider || seen[alias] {
			return
		}

		seen[alias] = true
		ordered = append(ordered, alias)
	}

	for _, name := range providerNames(configuration) {
		add(discovery.ProviderAlias{Name: name, Alias: ""})

		for _, alias := range configuration.ProviderAliases(name) {
			add(discovery.ProviderAlias{Name: name, Alias: alias})
		}
	}

	return ordered
}

// providerNames lists every provider local name the closure requires, sorted.
func providerNames(configuration discovery.Configuration) []string {
	seen := map[string]bool{}
	names := []string{}

	for _, module := range configuration.Modules {
		for _, provider := range module.Providers {
			if provider == discovery.BuiltinProvider || seen[provider] {
				continue
			}

			seen[provider] = true

			names = append(names, provider)
		}
	}

	slices.Sort(names)

	return names
}
