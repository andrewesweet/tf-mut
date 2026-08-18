package suggest_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The three fail-closed matrices, over the payload paths and provider types
// they are contracts about.
//
// This is the testing seam's fourth recorded exception, and it is the same
// exception `internal/fingerprint` already holds for the same reason: these are
// claims about documents and about published types, and driving the real binary
// into producing each of the fifteen shapes on demand is not possible. Every
// verdict the adapters lead to is still asserted through the engine seam.

// runIn builds the run block a suggestion would be placed in.
func runIn(name, moduleSource string) discovery.RunBlock {
	const file = "tests/unit.tftest.hcl"

	return discovery.RunBlock{
		Name: name, File: "/module/" + file, Rel: file,
		Command: discovery.CommandApply, ModuleSource: moduleSource,
		Assertions: 1, DefRange: hclRange(), Variables: nil,
		HasPlanTarget: false, JSONDeclared: false,
	}
}

func changeAt(path, baseline string) report.Change {
	return report.Change{
		Run: "tests/unit.tftest.hcl::applied", Path: path, Address: "",
		Baseline: baseline, Mutant: "", Sensitive: false,
	}
}

// schemaTyping publishes one attribute's cty type, which is the rendering
// contract's normative type source.
func schemaTyping(attribute, ctyType string) tfexec.Schemas {
	const resourceType = "example_thing"

	return tfexec.Schemas{
		FormatVersion: "1.0",
		ProviderSchemas: map[string]tfexec.ProviderSchema{
			"registry.terraform.io/example/example": {
				ResourceSchemas: map[string]tfexec.Schema{
					resourceType: {Block: tfexec.SchemaBlock{
						Attributes: map[string]tfexec.SchemaAttribute{
							attribute: {
								Required: false, Optional: true, Computed: false,
								Type: json.RawMessage(ctyType),
							},
						},
					}},
				},
				DataSourceSchemas: map[string]tfexec.Schema{},
			},
		},
	}
}

// untyped is the schema set of a module whose provider types nothing the
// generator asks about.
func untyped() tfexec.Schemas {
	return tfexec.Schemas{FormatVersion: "1.0", ProviderSchemas: map[string]tfexec.ProviderSchema{}}
}

const (
	statePrefix    = "root_module.resources["
	steadyBaseline = `"steady"`
)

// TestTheAddressAdapterMatrix is the seven mandatory address fixtures, quoted
// from the C2 disposition: "root resource; `count`; string-keyed `for_each`;
// nested collection element; root run observing a child only through an output;
// direct child-module run; wildcard/splat mapping failure". Each is one row.
func TestTheAddressAdapterMatrix(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		run      discovery.RunBlock
		path     string
		baseline string
		schemas  tfexec.Schemas
		want     string
		refused  error
	}{
		{
			name: "root resource", run: runIn("applied", ""),
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `example_thing.app.input == "steady"`, refused: nil,
		},
		{
			name: "count", run: runIn("applied", ""),
			path: statePrefix + "example_thing.app[0]].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `example_thing.app[0].input == "steady"`, refused: nil,
		},
		{
			name: "string-keyed for_each", run: runIn("applied", ""),
			path: statePrefix + `example_thing.app["blue"]].values.input`, baseline: steadyBaseline,
			schemas: untyped(), want: `example_thing.app["blue"].input == "steady"`, refused: nil,
		},
		{
			name: "nested collection element", run: runIn("applied", ""),
			path: statePrefix + "example_thing.app].values.items[1]", baseline: `"second"`,
			schemas: schemaTyping("items", `["list","string"]`),
			want:    `example_thing.app.items[1] == "second"`, refused: nil,
		},
		{
			name: "root run observing a child only through an output",
			run:  runIn("applied", ""),
			path: "outputs.from_child.value", baseline: steadyBaseline,
			schemas: untyped(), want: `output.from_child == "steady"`, refused: nil,
		},
		{
			name: "direct child-module run",
			run:  runIn("child", "./child"),
			path: statePrefix + "example_thing.inner].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `example_thing.inner.input == "steady"`, refused: nil,
		},
		{
			name: "wildcard/splat mapping failure", run: runIn("applied", ""),
			path: statePrefix + "example_thing.app[*]].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: "", refused: suggest.ErrUnaddressable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			expression, err := suggest.Express(testCase.run, testCase.schemas,
				changeAt(testCase.path, testCase.baseline))

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

// TestChildModuleInternalsAreNeverALegalAssertionSurface covers both payload
// spellings of the same fact: a root run can observe a child module only
// through the child's outputs.
func TestChildModuleInternalsAreNeverALegalAssertionSurface(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"resource_changes[module.child.example_thing.inner].change.after.input",
		"root_module.child_modules[0].resources[example_thing.inner].values.input",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			_, err := suggest.Express(runIn("applied", ""),
				untyped(), changeAt(path, steadyBaseline))

			if !errors.Is(err, suggest.ErrUnaddressable) {
				t.Fatalf("error = %v, want an unaddressable refusal", err)
			}
		})
	}
}

// TestAGeneratorLimitIsNeverARefutation states the C2 rule the whole status
// vocabulary exists for: every address the adapter declines is declined with
// its own error, never with one that could be read as verification evidence.
func TestAGeneratorLimitIsNeverARefutation(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		statePrefix + "example_thing.app[*]].values.input",
		"resource_changes[module.child.example_thing.inner].change.after.input",
		"outputs.thing.type",
		"resource_changes[example_thing.app].change.before.input",
	} {
		_, err := suggest.Express(runIn("applied", ""),
			untyped(), changeAt(path, steadyBaseline))

		if !errors.Is(err, suggest.ErrUnaddressable) {
			t.Fatalf("%s: error = %v, want an unaddressable refusal", path, err)
		}
	}
}

// TestTheRenderingContractMatrix gates the eight categories the C3 disposition
// names — "lists, tuples, sets, maps, objects, typed nulls, nested values,
// non-identifier map keys" — and TestTheRenderingContractAdmissions admits the
// three forms it allows.
func TestTheRenderingContractMatrix(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		path     string
		baseline string
		schemas  tfexec.Schemas
		want     string
		refused  error
	}{
		{
			name: "list element by concrete key, schema-typed",
			path: statePrefix + "example_thing.app].values.items[0]", baseline: `"first"`,
			schemas: schemaTyping("items", `["list","string"]`),
			want:    `example_thing.app.items[0] == "first"`, refused: nil,
		},
		{
			name: "tuple element by concrete key, schema-typed",
			path: statePrefix + "example_thing.app].values.pair[1]", baseline: "2",
			schemas: schemaTyping("pair", `["tuple",["string","number"]]`),
			want:    "example_thing.app.pair[1] == 2", refused: nil,
		},
		{
			name: "set element: a set has no index",
			path: statePrefix + "example_thing.app].values.names[0]", baseline: `"a"`,
			schemas: schemaTyping("names", `["set","string"]`),
			want:    "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "an untyped collection cannot be told from a set",
			path: statePrefix + "example_thing.app].values.items[0]", baseline: `"first"`,
			schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "map attribute", path: statePrefix + "example_thing.app].values.tags",
			baseline: `"not-a-map"`,
			schemas:  schemaTyping("tags", `["map","string"]`),
			want:     "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "object attribute", path: statePrefix + "example_thing.app].values.settings",
			baseline: `"not-an-object"`,
			schemas:  schemaTyping("settings", `["object",{"a":"string"}]`),
			want:     "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "typed null", path: statePrefix + "example_thing.app].values.input",
			baseline: "null", schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "nested value", path: statePrefix + "example_thing.app].values.settings.a",
			baseline: `"deep"`, schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			// A genuinely non-identifier key: HCL identifiers permit hyphens,
			// so only something like a space proves the row (round-3 review).
			name: "non-identifier map key", path: statePrefix + `example_thing.app].values.tags.Name Tag`,
			baseline: `"value"`, schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name:     "dotted map key is indistinguishable from a nested value",
			path:     statePrefix + "example_thing.app].values.tags.my.key",
			baseline: `"value"`, schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			expression, err := suggest.Express(runIn("applied", ""),
				testCase.schemas, changeAt(testCase.path, testCase.baseline))

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

// TestTheRenderingContractAdmissions is the matrix's other half: the three
// forms the contract admits, each rendering type-correctly.
func TestTheRenderingContractAdmissions(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		path     string
		baseline string
		schemas  tfexec.Schemas
		want     string
		refused  error
	}{
		{
			name: "scalar leaf where nothing types the attribute",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `example_thing.app.input == "steady"`, refused: nil,
		},
		{
			name: "number leaf", path: statePrefix + "example_thing.app].values.size",
			baseline: "3", schemas: untyped(), want: "example_thing.app.size == 3", refused: nil,
		},
		{
			name: "boolean leaf", path: statePrefix + "example_thing.app].values.enabled",
			baseline: "true", schemas: untyped(), want: "example_thing.app.enabled == true", refused: nil,
		},
		{
			name: "an empty collection is a length equality",
			path: statePrefix + "example_thing.app].values.items", baseline: "[]",
			schemas: untyped(), want: "length(example_thing.app.items) == 0", refused: nil,
		},
		{
			name: "an empty object is a length equality",
			path: statePrefix + "example_thing.app].values.tags", baseline: "{}",
			schemas: untyped(), want: "length(example_thing.app.tags) == 0", refused: nil,
		},
		{
			name: "a value absent from the baseline is not expressible",
			path: statePrefix + "example_thing.app].values.input", baseline: "",
			schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			expression, err := suggest.Express(runIn("applied", ""),
				testCase.schemas, changeAt(testCase.path, testCase.baseline))

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

// TestATosetAmbiguityIsAlwaysSkipped is C3's own example: the installed
// hclwrite renders a set and a list identically, Terraform's `toset(["a"]) ==
// ["a"]` is false, and the generator therefore never renders a collection.
func TestATosetAmbiguityIsAlwaysSkipped(t *testing.T) {
	t.Parallel()

	for _, ctyType := range []string{
		`["set","string"]`, `["list","string"]`, `["map","string"]`, `["object",{"a":"string"}]`,
	} {
		_, err := suggest.Express(runIn("applied", ""),
			schemaTyping("items", ctyType),
			changeAt(statePrefix+"example_thing.app].values.items", `"scalar-rendered"`))

		if !errors.Is(err, suggest.ErrUnrenderable) {
			t.Fatalf("%s: error = %v, want an unrenderable refusal", ctyType, err)
		}
	}
}

// TestTheSensitivityPredicateRefusesBeforeAnythingRenders covers the three
// cases the M3 disposition names — nested sensitive objects, sensitive outputs,
// and a sensitive value reached through a local — and proves the refusal
// carries the value nowhere.
func TestTheSensitivityPredicateRefusesBeforeAnythingRenders(t *testing.T) {
	t.Parallel()

	const secret = `"s3cret-value"`

	for _, path := range []string{
		statePrefix + "example_thing.app].values.input.hidden",
		"outputs.hidden.value",
		statePrefix + "example_thing.app].values.through_local",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			change := changeAt(path, secret)
			change.Sensitive = true

			expression, err := suggest.Express(
				runIn("applied", ""), untyped(), change)

			if !errors.Is(err, suggest.ErrSensitive) {
				t.Fatalf("error = %v, want a sensitivity refusal", err)
			}

			if expression != "" {
				t.Fatalf("a sensitive delta produced the expression %q", expression)
			}

			if contains(err.Error(), "s3cret-value") {
				t.Fatalf("the refusal carries the sensitive value: %v", err)
			}
		})
	}
}

func assertAdapter(t *testing.T, expression string, err error, want string, refused error) {
	t.Helper()

	if refused != nil {
		if !errors.Is(err, refused) {
			t.Fatalf("error = %v, want %v", err, refused)
		}

		if expression != "" {
			t.Fatalf("a refused adapter still produced %q", expression)
		}

		return
	}

	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	if expression != want {
		t.Fatalf("expression = %q, want %q", expression, want)
	}
}

// hclRange is the zero source range the fabricated run blocks carry: these
// tests are about payload paths and published types, not about source
// positions.
func hclRange() hcl.Range {
	return hcl.Range{Filename: "tests/unit.tftest.hcl", Start: hcl.InitialPos, End: hcl.InitialPos}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
