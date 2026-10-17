package suggest_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The three fail-closed matrices, over the payload paths and provider types
// they are contracts about.
//
// This is the testing seam's fourth recorded exception, and it is the same
// exception `internal/fingerprint` already holds for the same reason: these are
// claims about documents and about published types, and driving the real binary
// into producing each of the payload shapes on demand is not possible. Every
// verdict the adapters lead to is still asserted through the engine seam.

func changeAt(path, baseline string) fingerprint.Change {
	return fingerprint.Change{
		Run: "tests/unit.tftest.hcl::applied", Path: path, Address: "",
		Baseline: baseline, Mutant: "", Sensitive: false,
	}
}

// nulledAt is a change whose mutant side is null, the shape the non-null
// branch answers.
func nulledAt(path string) fingerprint.Change {
	change := changeAt(path, "")
	change.Mutant = nulledValue

	return change
}

// schemaTyping publishes one attribute's cty type, which is the rendering
// contract's normative type source.
func schemaTyping(attribute, ctyType string) tfexec.Schemas {
	return schemaBlock(map[string]string{attribute: ctyType}, nil)
}

// schemaBlock publishes a resource schema with the named attributes and nested
// block types, the shape the walk is a contract about.
func schemaBlock(
	attributes map[string]string,
	blockTypes map[string]tfexec.SchemaBlockType,
) tfexec.Schemas {
	const resourceType = "example_thing"

	published := map[string]tfexec.SchemaAttribute{}
	for name, ctyType := range attributes {
		published[name] = tfexec.SchemaAttribute{
			Required: false, Optional: true, Computed: false,
			Type: json.RawMessage(ctyType),
		}
	}

	return tfexec.Schemas{
		FormatVersion: "1.0",
		ProviderSchemas: map[string]tfexec.ProviderSchema{
			"registry.terraform.io/example/example": {
				ResourceSchemas: map[string]tfexec.Schema{
					resourceType: {Block: tfexec.SchemaBlock{
						Attributes: published,
						BlockTypes: blockTypes,
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
	statePrefix            = "root_module.resources["
	steadyBaseline         = `"steady"`
	emptiedObject          = "{}"
	secondValue            = `"second"`
	countedResource        = "example_thing.counted"
	nestedMarker           = `"deep"`
	nulledValue            = "null"
	aliasesAttribute       = "aliases"
	baselineCountAssertion = "length(example_thing.counted) == 2"
	firstElementIndex      = "[0]"
	connectionBlock        = "connection"
	steadyTryEquality      = `try(example_thing.app.input, null) == "steady"`
	firstBaseline          = `"first"`
	shortNameAttr          = "short_name"
	mountsBlock            = "mounts"
	exampleResource        = "example_thing.app"
	timeoutBaseline        = `"5m"`
)

// TestTheAddressAdapterMatrix is the seven mandatory address fixtures, quoted
// from the C2 disposition: "root resource; `count`; string-keyed `for_each`;
// nested collection element; root run observing a child only through an output;
// direct child-module run; wildcard/splat mapping failure". Each is one row.
func TestTheAddressAdapterMatrix(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		path       string
		baseline   string
		schemas    tfexec.Schemas
		outputType cty.Type
		want       string
		refused    error
	}{
		{
			name: "root resource",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: steadyTryEquality, refused: nil,
		},
		{
			name: "count",
			path: statePrefix + "example_thing.app[0]].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `try(example_thing.app[0].input, null) == "steady"`, refused: nil,
		},
		{
			name: "string-keyed for_each",
			path: statePrefix + `example_thing.app["blue"]].values.input`, baseline: steadyBaseline,
			schemas: untyped(), want: `try(example_thing.app["blue"].input, null) == "steady"`, refused: nil,
		},
		{
			name: "nested collection element",
			path: statePrefix + "example_thing.app].values.items[1]", baseline: secondValue,
			schemas: schemaTyping("items", `["list","string"]`),
			want:    `try(example_thing.app.items[1], null) == "second"`, refused: nil,
		},
		{
			name: "root run observing a child only through an output",
			path: "outputs.from_child.value", baseline: steadyBaseline,
			schemas: untyped(), want: `output.from_child == "steady"`, refused: nil,
		},
		{
			name:       "a fused name-and-index segment renders in index form",
			outputType: cty.List(cty.Object(map[string]cty.Type{aliasesAttribute: cty.List(cty.String)})),
			path:       "outputs.from_child.value[0].aliases[0]",
			baseline:   steadyBaseline,
			schemas:    untyped(), want: `try(output.from_child[0].aliases[0], null) == "steady"`, refused: nil,
		},
		{
			name:     "two names after an output element cannot be told apart from map keys",
			path:     "outputs.from_child.value[0].inner.leaf",
			baseline: steadyBaseline,
			schemas:  untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name:       "a list-valued output's element",
			outputType: cty.List(cty.Object(map[string]cty.Type{shortNameAttr: cty.String})),
			path:       "outputs.from_child.value[0].short_name", baseline: steadyBaseline,
			schemas: untyped(), want: `try(output.from_child[0].short_name, null) == "steady"`, refused: nil,
		},
		{
			name:       "a list-valued output's element in a plan payload",
			outputType: cty.List(cty.Object(map[string]cty.Type{shortNameAttr: cty.String})),
			path:       "output_changes.from_child.after[1].short_name", baseline: steadyBaseline,
			schemas: untyped(), want: `try(output.from_child[1].short_name, null) == "steady"`, refused: nil,
		},
		{
			name: "direct child-module run",
			path: statePrefix + "example_thing.inner].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: `try(example_thing.inner.input, null) == "steady"`, refused: nil,
		},
		{
			name: "wildcard/splat mapping failure",
			path: statePrefix + "example_thing.app[*]].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: "", refused: suggest.ErrUnaddressable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			expression, err := suggest.Express(testCase.schemas,
				suggest.Evidence{OutputTypes: map[string]cty.Type{"output.from_child": testCase.outputType}},
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

			_, err := suggest.Express(
				untyped(), suggest.Evidence{}, changeAt(path, steadyBaseline),
			)

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
		_, err := suggest.Express(
			untyped(), suggest.Evidence{}, changeAt(path, steadyBaseline),
		)

		if !errors.Is(err, suggest.ErrUnaddressable) {
			t.Fatalf("%s: error = %v, want an unaddressable refusal", path, err)
		}
	}
}

// TestTheRenderingContractMatrix gates the eight categories the C3 disposition
// names — "lists, tuples, sets, maps, objects, typed nulls, nested values,
// non-identifier map keys". TestTheRenderingContractAdmissions covers the
// supported scalar, keyed, nested-block, null, emptiness and count forms.
// renderingCase is one row of the rendering contract tables.
type renderingCase struct {
	name     string
	path     string
	baseline string
	mutant   string
	evidence suggest.Evidence
	schemas  tfexec.Schemas
	want     string
	refused  error
}

// matrixRows is the refusal half of the rendering contract: shapes the
// contract refuses, one row each.
func matrixRows() []renderingCase {
	return []renderingCase{
		{
			name: "list element by concrete key, schema-typed",
			path: statePrefix + "example_thing.app].values.items[0]", baseline: firstBaseline,
			schemas: schemaTyping("items", `["list","string"]`),
			want:    `try(example_thing.app.items[0], null) == "first"`, refused: nil,
		},
		{
			name: "tuple element by concrete key, schema-typed",
			path: statePrefix + "example_thing.app].values.pair[1]", baseline: "2",
			schemas: schemaTyping("pair", `["tuple",["string","number"]]`),
			want:    "try(example_thing.app.pair[1], null) == 2", refused: nil,
		},
		{
			name: "set element: a set has no index",
			path: statePrefix + "example_thing.app].values.names[0]", baseline: `"a"`,
			schemas: schemaTyping("names", `["set","string"]`),
			want:    "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "an untyped collection cannot be told from a set",
			path: statePrefix + "example_thing.app].values.items[0]", baseline: firstBaseline,
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
			baseline: nulledValue, schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "nested value", path: statePrefix + "example_thing.app].values.settings.a",
			baseline: nestedMarker, schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
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
		{
			name: "a dynamic attribute types nothing beyond itself",
			path: statePrefix + "example_thing.app].values.manifest[0].spec", baseline: nestedMarker,
			schemas: schemaTyping("manifest", `"dynamic"`),
			want:    "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "an unknown name inside a typed block stays a nested value",
			path: statePrefix + "example_thing.app].values.settings.unknown", baseline: nestedMarker,
			schemas: schemaTyping("settings", `["object",{"a":"string"}]`),
			want:    "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "a nested map-in-map key is ambiguous",
			path: statePrefix + "example_thing.app].values.matrix.a.b.c", baseline: nestedMarker,
			schemas: schemaTyping("matrix", `["map",["map","string"]]`),
			want:    "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "a set-nested block cannot be indexed",
			path: statePrefix + "example_thing.app].values.replicas[0].arn", baseline: nestedMarker,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				"replicas": {NestingMode: tfexec.NestingSet, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"arn": {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
			}),
			want: "", refused: suggest.ErrUnrenderable,
		},
	}
}

// admissionRows combines the matrix's scalar and structured assertion forms,
// each rendering type-correctly.
func admissionRows() []renderingCase {
	return append(scalarAdmissionRows(), structuredAdmissionRows()...)
}

// scalarAdmissionRows holds the admitted scalar forms.
func scalarAdmissionRows() []renderingCase {
	return append(templateKeyRows(), scalarKeyRows()...)
}

// templateKeyRows holds the admitted forms whose keys carry HCL template
// syntax.
func templateKeyRows() []renderingCase {
	return []renderingCase{
		{
			name: "scalar leaf where nothing types the attribute",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: steadyTryEquality, refused: nil,
		},
		{
			name: "number leaf", path: statePrefix + "example_thing.app].values.size",
			baseline: "3", schemas: untyped(), want: "try(example_thing.app.size, null) == 3", refused: nil,
		},
		{
			name: "boolean leaf", path: statePrefix + "example_thing.app].values.enabled",
			baseline: "true", schemas: untyped(), want: "try(example_thing.app.enabled, null) == true", refused: nil,
		},
		{
			name: "an empty collection is a length equality",
			path: statePrefix + "example_thing.app].values.items", baseline: "[]",
			schemas: untyped(), want: "try(length(example_thing.app.items), null) == 0", refused: nil,
		},
		{
			name: "an empty object is a length equality",
			path: statePrefix + "example_thing.app].values.tags", baseline: "{}",
			schemas: untyped(), want: "try(length(example_thing.app.tags), null) == 0", refused: nil,
		},
		{
			name: "a value absent from the baseline is not expressible",
			path: statePrefix + "example_thing.app].values.input", baseline: "",
			schemas: untyped(), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name:     "a list-nested block's attribute",
			path:     statePrefix + "example_thing.app].values.connection[0].subresource_names[0]",
			baseline: secondValue,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				connectionBlock: {NestingMode: tfexec.NestingList, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"subresource_names": {Optional: true, Type: json.RawMessage(`["list","string"]`)},
					},
				}},
			}),
			want:    `try(example_thing.app.connection[0].subresource_names[0], null) == "second"`,
			refused: nil,
		},
		{
			name: "a single-nested block's attribute",
			path: statePrefix + "example_thing.app].values.timeouts.create", baseline: timeoutBaseline,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				"timeouts": {NestingMode: tfexec.NestingSingle, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"create": {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
			}),
			want: `try(example_thing.app.timeouts.create, null) == "5m"`, refused: nil,
		},
		{
			name: "a typed object attribute's list element",
			path: statePrefix + "example_thing.app].values.spec[0].replicas", baseline: "3",
			schemas: schemaTyping("spec",
				`["list",["object",{"replicas":"number","name":"string"}]]`),
			want: "try(example_thing.app.spec[0].replicas, null) == 3", refused: nil,
		},
		{
			name:     "a keyed entry whose key carries template syntax stays literal",
			path:     statePrefix + "example_thing.app].values.triggers.${part}%{blk}",
			baseline: steadyBaseline,
			schemas:  schemaTyping("triggers", `["map","string"]`),
			want:     `try(example_thing.app.triggers["$${part}%%{blk}"], null) == "steady"`,
			refused:  nil,
		},
	}
}

// scalarKeyRows holds the admitted scalar and keyed-scalar forms.
func scalarKeyRows() []renderingCase {
	return []renderingCase{
		{
			name:     "a map attribute's non-identifier key",
			path:     statePrefix + "example_thing.app].values.triggers.app.kubernetes.io/part-of",
			baseline: steadyBaseline,
			schemas:  schemaTyping("triggers", `["map","string"]`),
			want:     `try(example_thing.app.triggers["app.kubernetes.io/part-of"], null) == "steady"`,
			refused:  nil,
		},
	}
}

// structuredAdmissionRows holds the admitted container, nested and count
// forms.
func structuredAdmissionRows() []renderingCase {
	return append(nullFormRows(), keyAndBlockRows()...)
}

// nullFormRows holds the admitted null and emptiness forms over spelled
// continuations.
func nullFormRows() []renderingCase {
	return []renderingCase{
		{
			name:     "a nulled keyed entry renders in index form",
			path:     statePrefix + "example_thing.app].values.triggers.app.io/x",
			baseline: steadyBaseline, mutant: nulledValue,
			schemas: schemaTyping("triggers", `["map","string"]`),
			want:    `try(example_thing.app.triggers["app.io/x"], null) != null`, refused: nil,
		},
		{
			name:     "a nulled nested block attribute renders through the walk",
			path:     statePrefix + "example_thing.app].values.connection[0].subresource_names[0]",
			baseline: secondValue, mutant: nulledValue,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				connectionBlock: {NestingMode: tfexec.NestingList, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"subresource_names": {Optional: true, Type: json.RawMessage(`["list","string"]`)},
					},
				}},
			}),
			want:    `try(example_thing.app.connection[0].subresource_names[0], null) != null`,
			refused: nil,
		},
		{
			name:     "an emptied keyed entry is a length over the index form",
			path:     statePrefix + "example_thing.app].values.triggers.app.io/x",
			baseline: emptiedObject, mutant: "",
			schemas: schemaTyping("triggers", `["map","string"]`),
			want:    `try(length(example_thing.app.triggers["app.io/x"]), null) == 0`, refused: nil,
		},
	}
}

// keyAndBlockRows holds the admitted keyed and nested-block forms.
func keyAndBlockRows() []renderingCase {
	return append(mapBlockRows(), keyRows()...)
}

// mapBlockRows holds the admitted and refused map-nested block forms.
func mapBlockRows() []renderingCase {
	return []renderingCase{
		{
			name: "a map-nested block's entry by its dotted payload key",
			path: statePrefix + "example_thing.app].values.mounts.shared.source", baseline: `"/data"`,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				mountsBlock: {NestingMode: tfexec.NestingMap, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"source": {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
			}),
			want: `try(example_thing.app.mounts["shared"].source, null) == "/data"`, refused: nil,
		},
		{
			name: "a map-nested block whose path stops at the block is refused",
			path: statePrefix + "example_thing.app].values.mounts.shared", baseline: `"/data"`,
			schemas: schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				mountsBlock: {NestingMode: tfexec.NestingMap, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"source": {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
			}),
			want: "", refused: suggest.ErrUnrenderable,
		},
	}
}

// keyRows holds the admitted keyed and scalar forms.
func keyRows() []renderingCase {
	return []renderingCase{
		{
			name: "a map attribute's identifier key stays dotted",
			path: statePrefix + "example_thing.app].values.triggers.plain", baseline: steadyBaseline,
			schemas: schemaTyping("triggers", `["map","string"]`),
			want:    `try(example_thing.app.triggers.plain, null) == "steady"`, refused: nil,
		},
		{
			name: "an object attribute's non-identifier key",
			path: statePrefix + "example_thing.app].values.settings.app.io/x", baseline: steadyBaseline,
			schemas: schemaTyping("settings", `["object",{"app.io/x":"string","plain":"string"}]`),
			want:    `try(example_thing.app.settings["app.io/x"], null) == "steady"`, refused: nil,
		},
		{
			name: "a map attribute's numeric key is not an index",
			path: statePrefix + "example_thing.app].values.tags.0", baseline: steadyBaseline,
			schemas: schemaTyping("tags", `["map","string"]`),
			want:    `try(example_thing.app.tags["0"], null) == "steady"`, refused: nil,
		},
		{
			name: "a nulled whole output asserts non-null",
			path: "outputs.identity.value", baseline: "", mutant: nulledValue,
			schemas: untyped(), want: "output.identity != null", refused: nil,
		},
		{
			name: "a nulled leaf with a baseline value asserts non-null",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			mutant: nulledValue, schemas: untyped(),
			want: "try(example_thing.app.input, null) != null", refused: nil,
		},
		{
			name: "a nulled container the delta proves non-null asserts non-null",
			path: statePrefix + "example_thing.app].values.triggers", baseline: "", mutant: nulledValue,
			schemas: schemaTyping("triggers", `["map","string"]`),
			evidence: suggest.NewEvidence([]fingerprint.Change{
				nulledAt(statePrefix + "example_thing.app].values.triggers"),
				changeAt(statePrefix+"example_thing.app].values.triggers.plain", steadyBaseline),
			}),
			want: "try(example_thing.app.triggers, null) != null", refused: nil,
		},
		{
			name: "a vanished value without neighbourhood proof stays refused",
			path: statePrefix + "example_thing.app].values.triggers", baseline: "", mutant: nulledValue,
			schemas: schemaTyping("triggers", `["map","string"]`), want: "", refused: suggest.ErrUnrenderable,
		},
		{
			name: "a length is never rendered from unknown elements",
			path: "output_changes.identity.after", baseline: "{}", mutant: `"2"`,
			schemas:  untyped(),
			evidence: suggest.Evidence{UnknownMarkers: []string{"output_changes.identity.after_unknown.principal_id"}},
			want:     "", refused: suggest.ErrUnrenderable,
		},
		{
			name:     "a vanished zeroth instance is a count equality",
			path:     statePrefix + "example_thing.counted[0]].values.input",
			baseline: steadyBaseline, mutant: "",
			schemas:  untyped(),
			evidence: suggest.Evidence{InstanceCounts: map[string]int{countedResource: 2}},
			want:     baselineCountAssertion, refused: nil,
		},
		{
			name: "a vanished count instance is a count equality",
			path: statePrefix + "example_thing.counted[1]].values.input", baseline: steadyBaseline, mutant: "",
			schemas:  untyped(),
			evidence: suggest.Evidence{InstanceCounts: map[string]int{countedResource: 2}},
			want:     baselineCountAssertion, refused: nil,
		},
		{
			name: "an index above the proven count is no count equality",
			path: statePrefix + `example_thing.app["blue"]].values.input`, baseline: steadyBaseline, mutant: "",
			schemas:  untyped(),
			evidence: suggest.Evidence{InstanceCounts: map[string]int{exampleResource: 2}},
			want:     `try(example_thing.app["blue"].input, null) == "steady"`, refused: nil,
		},
	}
}

func TestTheRenderingContractMatrix(t *testing.T) {
	t.Parallel()

	for _, testCase := range matrixRows() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			expression, err := suggest.Express(
				testCase.schemas, suggest.Evidence{}, changeAt(testCase.path, testCase.baseline),
			)

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

// TestTheRenderingContractAdmissions covers the admitted scalar and structured
// assertion forms, each rendering type-correctly.
func TestTheRenderingContractAdmissions(t *testing.T) {
	t.Parallel()

	for _, testCase := range admissionRows() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			change := changeAt(testCase.path, testCase.baseline)
			change.Mutant = testCase.mutant

			expression, err := suggest.Express(
				testCase.schemas, testCase.evidence, change,
			)

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

// TestAbsenceTolerantFormsFailRatherThanError is the D14 contract: an
// assertion that reads a map key or an attribute renders in a form that turns
// a missing key, attribute or instance into a plain failure through try
// with a null default, while a whole value keeps its direct equality.
// Only a test failure verifies a suggestion, so an erroring form is a
// refutation the report must never publish for these shapes.
func TestAbsenceTolerantFormsFailRatherThanError(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		path     string
		baseline string
		mutant   string
		evidence suggest.Evidence
		schemas  tfexec.Schemas
		want     string
		refused  error
	}{
		{
			name:     "a map key reads through try with a null default",
			path:     statePrefix + "example_thing.app].values.triggers.app.kubernetes.io/part-of",
			baseline: steadyBaseline,
			schemas:  schemaTyping("triggers", `["map","string"]`),
			want:     `try(example_thing.app.triggers["app.kubernetes.io/part-of"], null) == "steady"`,
		},
		{
			name: "an object key reads through try with a null default",
			path: statePrefix + "example_thing.app].values.settings.app.io/x", baseline: steadyBaseline,
			schemas: schemaTyping("settings", `["object",{"app.io/x":"string","plain":"string"}]`),
			want:    `try(example_thing.app.settings["app.io/x"], null) == "steady"`,
		},
		{
			name: "a deleted attribute reads through try with a null default",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			schemas: untyped(), want: steadyTryEquality,
		},
		{
			name: "a numeric index reads through try with a null default",
			path: statePrefix + "example_thing.app].values.items[0]", baseline: firstBaseline,
			schemas: schemaTyping("items", `["list","string"]`),
			want:    `try(example_thing.app.items[0], null) == "first"`,
		},
		{
			name:     "a list-valued output element reads through try with a null default",
			path:     "outputs.regions.value[0].short_name",
			baseline: `"wu"`,
			evidence: suggest.Evidence{OutputTypes: map[string]cty.Type{
				"output.regions": cty.List(cty.Object(map[string]cty.Type{shortNameAttr: cty.String})),
			}},
			schemas: untyped(), want: `try(output.regions[0].short_name, null) == "wu"`,
		},
		{
			name: "a whole output value keeps its direct equality",
			path: "outputs.ignored.value", baseline: `"nobody-checks-me"`,
			schemas: untyped(), want: `output.ignored == "nobody-checks-me"`,
		},
		{
			name: "a nulled attribute answers through try",
			path: statePrefix + "example_thing.app].values.input", baseline: steadyBaseline,
			mutant: nulledValue, schemas: untyped(),
			want: `try(example_thing.app.input, null) != null`,
		},
		{
			name:     "a nulled keyed entry answers through try",
			path:     statePrefix + "example_thing.app].values.triggers.app.io/x",
			baseline: steadyBaseline, mutant: nulledValue,
			schemas: schemaTyping("triggers", `["map","string"]`),
			want:    `try(example_thing.app.triggers["app.io/x"], null) != null`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			change := changeAt(testCase.path, testCase.baseline)
			change.Mutant = testCase.mutant

			expression, err := suggest.Express(testCase.schemas, testCase.evidence, change)

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

func TestEmptyCollectionAssertionsEvaluateAbsenceAsFalse(t *testing.T) {
	t.Parallel()

	const labelsAttribute = "labels"

	for name, test := range map[string]struct {
		baseline string
		empty    cty.Value
		full     cty.Value
	}{
		"array": {
			baseline: "[]", empty: cty.EmptyTupleVal, full: cty.TupleVal([]cty.Value{cty.StringVal("item")}),
		},
		"map": {
			baseline: "{}", empty: cty.MapValEmpty(cty.String),
			full: cty.MapVal(map[string]cty.Value{"item": cty.StringVal("value")}),
		},
	} {
		for _, prefix := range []string{"outputs.settings.value", "output_changes.settings.after"} {
			t.Run(name+"/"+prefix, func(t *testing.T) {
				t.Parallel()

				expression, err := suggest.Express(untyped(), suggest.Evidence{}, changeAt(prefix+".labels", test.baseline))
				if err != nil {
					t.Fatalf("rendering: %v", err)
				}

				parsed, diagnostics := discovery.ParseExpression([]byte(expression), "assertion.tftest.hcl", hcl.InitialPos)
				if diagnostics.HasErrors() {
					t.Fatalf("parsing %q: %s", expression, diagnostics)
				}

				for state, settings := range map[string]cty.Value{
					"empty":            cty.ObjectVal(map[string]cty.Value{labelsAttribute: test.empty}),
					"nonempty":         cty.ObjectVal(map[string]cty.Value{labelsAttribute: test.full}),
					"null member":      cty.ObjectVal(map[string]cty.Value{labelsAttribute: cty.NullVal(test.empty.Type())}),
					"missing member":   cty.EmptyObjectVal,
					"missing ancestor": cty.NullVal(cty.Object(map[string]cty.Type{labelsAttribute: test.empty.Type()})),
				} {
					value, diagnostics := parsed.Value(&hcl.EvalContext{
						Variables: map[string]cty.Value{
							"output": cty.ObjectVal(map[string]cty.Value{"settings": settings}),
						},
						Functions: map[string]function.Function{"try": tryfunc.TryFunc, "length": stdlib.LengthFunc},
					})
					if diagnostics.HasErrors() || !value.RawEquals(cty.BoolVal(state == "empty")) {
						t.Fatalf("%s: %s evaluated to %s, diagnostics: %s", state, expression, value.GoString(), diagnostics)
					}
				}
			})
		}
	}
}

func TestKeyedAssertionsFailWhenTheirInstanceDisappears(t *testing.T) {
	t.Parallel()

	const (
		triggersAttribute = "triggers"
		instanceKey       = "blue"
		mapStringType     = `["map","string"]`
	)

	keyed := cty.MapVal(map[string]cty.Value{"app.io/x": cty.StringVal("steady")})
	for name, test := range map[string]struct {
		path      string
		mutant    string
		valueType string
		value     cty.Value
	}{
		"leaf equality": {
			path:      statePrefix + `example_thing.app["blue"]].values.triggers`,
			valueType: `"string"`, value: cty.StringVal("steady"),
		},
		"continued equality": {
			path:      statePrefix + `example_thing.app["blue"]].values.triggers.app.io/x`,
			valueType: mapStringType, value: keyed,
		},
		"non-null": {
			path: statePrefix + `example_thing.app["blue"]].values.triggers.app.io/x`, mutant: nulledValue,
			valueType: mapStringType, value: keyed,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			change := changeAt(test.path, steadyBaseline)
			change.Mutant = test.mutant
			expression, err := suggest.Express(schemaTyping(triggersAttribute, test.valueType), suggest.Evidence{}, change)
			if err != nil {
				t.Fatalf("rendering: %v", err)
			}

			parsed, diagnostics := discovery.ParseExpression([]byte(expression), "assertion.tftest.hcl", hcl.InitialPos)
			if diagnostics.HasErrors() {
				t.Fatalf("parsing %q: %s", expression, diagnostics)
			}

			present := cty.ObjectVal(map[string]cty.Value{triggersAttribute: test.value})
			states := map[string]cty.Value{
				"present": cty.MapVal(map[string]cty.Value{instanceKey: present}),
				"null attribute": cty.MapVal(map[string]cty.Value{instanceKey: cty.ObjectVal(map[string]cty.Value{
					triggersAttribute: cty.NullVal(test.value.Type()),
				})}),
				"missing instance": cty.MapValEmpty(present.Type()),
			}
			if test.value.Type().IsMapType() {
				states["missing key"] = cty.MapVal(map[string]cty.Value{instanceKey: cty.ObjectVal(map[string]cty.Value{
					triggersAttribute: cty.MapValEmpty(cty.String),
				})})
			}

			for state, instances := range states {
				value, diagnostics := parsed.Value(&hcl.EvalContext{
					Variables: map[string]cty.Value{
						"example_thing": cty.ObjectVal(map[string]cty.Value{"app": instances}),
					},
					Functions: map[string]function.Function{"try": tryfunc.TryFunc, "lookup": stdlib.LookupFunc},
				})
				if diagnostics.HasErrors() || !value.RawEquals(cty.BoolVal(state == "present")) {
					t.Fatalf("%s: %s evaluated to %s, diagnostics: %s", state, expression, value.GoString(), diagnostics)
				}
			}
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
		_, err := suggest.Express(
			schemaTyping("items", ctyType),
			suggest.Evidence{}, changeAt(statePrefix+"example_thing.app].values.items", `"scalar-rendered"`),
		)

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
				untyped(), suggest.Evidence{}, change,
			)

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

// TestAValueThePlanMarksUnknownIsRefusedUnderPlan is the plan-unknown
// contract: under a `command = plan` run, a known-to-unknown delta whose
// value side the mutant's own after_unknown record covers renders in no
// equality — the replayed assertion would error with "Unknown condition
// value" — while the same shapes under apply, and the same delta without
// markers, render unchanged. The resource-attribute deletion and the indexed
// marker shapes are the ones the offline engine fixture cannot stage; the
// engine seam carries the verdict those shapes lead to.
func TestAValueThePlanMarksUnknownIsRefusedUnderPlan(t *testing.T) {
	t.Parallel()

	for _, testCase := range planUnknownRows() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			change := changeAt(testCase.path, testCase.baseline)
			change.Mutant = testCase.mutant
			evidence := suggest.Evidence{
				OutputTypes:    testCase.types,
				UnknownMarkers: testCase.markers,
				Plan:           testCase.plan,
			}

			expression, err := suggest.Express(testCase.schemas, evidence, change)

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
			if testCase.refused != nil && err != nil {
				if !contains(err.Error(), "unknown at plan") {
					t.Fatalf("the refusal does not say the value is unknown at plan: %v", err)
				}

				if !contains(err.Error(), testCase.path) {
					t.Fatalf("the refusal does not name the value %s: %v", testCase.path, err)
				}
			}
		})
	}
}

// planUnknownCase is one plan-unknown matrix row.
type planUnknownCase = struct {
	name     string
	path     string
	baseline string
	mutant   string
	markers  []string
	plan     bool
	types    map[string]cty.Type
	schemas  tfexec.Schemas
	want     string
	refused  error
}

// planUnknownRows holds the plan-unknown refusals and their render-everywhere-
// else controls.
func planUnknownRows() []planUnknownCase {
	const deletedAttr = "resource_changes[example_thing.app].change.after.input"
	const elementValue = `"app"`

	return []planUnknownCase{
		{
			name:     "a deleted resource attribute under plan is refused",
			path:     deletedAttr,
			baseline: steadyBaseline, mutant: "",
			markers: []string{"resource_changes[example_thing.app].change.after_unknown.input"},
			plan:    true,
			schemas: schemaTyping("input", `"string"`),
			refused: suggest.ErrUnrenderable,
		},
		{
			name:     "a counted resource marker covers its attribute",
			path:     "resource_changes[example_thing.app[0]].change.after.input",
			baseline: steadyBaseline, mutant: "",
			markers: []string{"resource_changes[example_thing.app[0]].change.after_unknown.input"},
			plan:    true,
			schemas: schemaTyping("input", `"string"`),
			refused: suggest.ErrUnrenderable,
		},
		{
			name:     "a keyed resource marker covers its attribute",
			path:     `resource_changes[example_thing.app["primary"]].change.after.input`,
			baseline: steadyBaseline, mutant: "",
			markers: []string{`resource_changes[example_thing.app["primary"]].change.after_unknown.input`},
			plan:    true,
			schemas: schemaTyping("input", `"string"`),
			refused: suggest.ErrUnrenderable,
		},
		{
			name:     "the same deleted attribute under apply renders",
			path:     deletedAttr,
			baseline: steadyBaseline, mutant: "",
			schemas: schemaTyping("input", `"string"`),
			want:    `try(example_thing.app.input, null) == "steady"`,
		},
		{
			name:     "a vanished value with no marker under plan still renders",
			path:     deletedAttr,
			baseline: steadyBaseline, mutant: "",
			plan:     true,
			schemas:  schemaTyping("input", `"string"`),
			want:     `try(example_thing.app.input, null) == "steady"`,
		},
		{
			name:     "an indexed marker covers the element's member",
			path:     "output_changes.region_list.after[0].short_name",
			baseline: elementValue, mutant: "",
			markers: []string{"output_changes.region_list.after_unknown[0].short_name"},
			plan: true,
			types: map[string]cty.Type{
				"output.region_list": cty.List(cty.Object(map[string]cty.Type{shortNameAttr: cty.String})),
			},
			schemas: untyped(),
			refused: suggest.ErrUnrenderable,
		},
		{
			name:     "an indexed marker covers the whole element",
			path:     "output_changes.list.after[0]",
			baseline: elementValue, mutant: "",
			markers: []string{"output_changes.list.after_unknown[0]"},
			plan:     true,
			types:    map[string]cty.Type{"output.list": cty.List(cty.String)},
			schemas:  untyped(),
			refused:  suggest.ErrUnrenderable,
		},
		{
			name:     "a member of an indexed element is covered",
			path:     "output_changes.list.after[0].id",
			baseline: elementValue, mutant: "",
			markers: []string{"output_changes.list.after_unknown[0].id"},
			plan: true,
			types: map[string]cty.Type{
				"output.list": cty.List(cty.Object(map[string]cty.Type{"id": cty.String})),
			},
			schemas: untyped(),
			refused: suggest.ErrUnrenderable,
		},
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

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
