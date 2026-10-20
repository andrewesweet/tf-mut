package suggest_test

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

const (
	regionsStateValue = "outputs.regions.value"
	regionsPlanAfter  = "output_changes.regions.after"
	sourceAttribute   = "source"
	emptyForm         = "empty"
	settingsBlock     = "settings"
	stringListType    = `["list","string"]`
)

func TestTypedMemberKeysPreserveTheirLiteralSpelling(t *testing.T) {
	t.Parallel()

	for _, key := range []struct{ value, quoted string }{
		{"cost$", `"cost$"`},
		{"tax%", `"tax%"`},
		{"${part}%{blk}", `"$${part}%%{blk}"`},
		{"a\b\f\v\ab", `"a\u0008\u000c\u000b\u0007b"`},
		{"[prod]", `"[prod]"`},
		{firstElementIndex, `"[0]"`},
	} {
		for _, declared := range []cty.Type{cty.Map(cty.String), cty.Object(map[string]cty.Type{key.value: cty.String})} {
			t.Run(key.quoted+"/"+declared.FriendlyName(), func(t *testing.T) {
				t.Parallel()

				wire, err := ctyjson.MarshalType(declared)
				if err != nil {
					t.Fatal(err)
				}

				for _, prefix := range []string{
					statePrefix + "example_thing.app].values.tags",
					"resource_changes[example_thing.app].change.after.tags",
					regionsStateValue, regionsPlanAfter,
				} {
					resource := "example_thing.app.tags"
					if fingerprint.ParsePath(prefix).Address() == regionsOutput {
						resource = regionsOutput
					}

					expression, err := suggest.Express(schemaTyping("tags", string(wire)),
						suggest.Evidence{OutputTypes: map[string]cty.Type{regionsOutput: declared}},
						changeAt(prefix+"."+key.value, steadyBaseline))
					assertAdapter(t, expression, err, "try("+resource+"["+key.quoted+"], null) == "+steadyBaseline, nil)
				}
			})
		}

		t.Run("block/"+key.quoted, func(t *testing.T) {
			t.Parallel()

			schemas := schemaBlock(nil, map[string]tfexec.SchemaBlockType{
				mountsBlock: {NestingMode: tfexec.NestingMap, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						sourceAttribute: {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
			})
			expression, err := suggest.Express(schemas, suggest.Evidence{},
				changeAt(statePrefix+"example_thing.app].values.mounts."+key.value+".source", steadyBaseline))
			assertAdapter(t, expression, err,
				"try(example_thing.app.mounts["+key.quoted+"].source, null) == "+steadyBaseline, nil)
		})
	}
}

func TestRecursiveAmbiguityNeverSelectsAnOuterLiteralKey(t *testing.T) {
	t.Parallel()

	for _, form := range []struct{ name, baseline, mutant string }{
		{"scalar", steadyBaseline, ""},
		{"null", steadyBaseline, nulledValue},
		{emptyForm, "[]", ""},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()

			leaf := cty.String
			if form.name == emptyForm {
				leaf = cty.List(cty.String)
			}
			declared := cty.Object(map[string]cty.Type{
				"a":      cty.Object(map[string]cty.Type{"b": cty.List(leaf), "b[0]": leaf}),
				"a.b[0]": leaf,
			})
			wire, err := ctyjson.MarshalType(declared)
			if err != nil {
				t.Fatal(err)
			}

			for _, prefix := range []string{
				statePrefix + "example_thing.app].values.tags", regionsStateValue, regionsPlanAfter,
			} {
				change := changeAt(prefix+".a.b[0]", form.baseline)
				change.Mutant = form.mutant
				expression, err := suggest.Express(schemaTyping("tags", string(wire)),
					suggest.Evidence{OutputTypes: map[string]cty.Type{regionsOutput: declared}}, change)
				assertAdapter(t, expression, err, "", suggest.ErrUnrenderable)
			}
		})
	}
}

func TestOutputContainersUseStructuralNonNullEvidence(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{regionsStateValue, regionsPlanAfter} {
		for _, suffix := range []string{".member", firstElementIndex} {
			for _, baseline := range []string{steadyBaseline, nulledValue} {
				t.Run(prefix+suffix+baseline, func(t *testing.T) {
					t.Parallel()

					nulled := nulledAt(prefix)
					evidence := suggest.NewEvidence([]fingerprint.Change{nulled, changeAt(prefix+suffix, baseline)})
					expression, err := suggest.Express(untyped(), evidence, nulled)
					assertAdapter(t, expression, err, "output.regions != null", nil)
				})
			}
		}
	}
	for _, suffix := range []string{".member", firstElementIndex} {
		t.Run("unknown"+suffix, func(t *testing.T) {
			t.Parallel()

			nulled := nulledAt(regionsPlanAfter)
			evidence := suggest.NewEvidence([]fingerprint.Change{
				nulled, changeAt("output_changes.regions.after_unknown"+suffix, "true"),
			})
			expression, err := suggest.Express(untyped(), evidence, nulled)
			assertAdapter(t, expression, err, "output.regions != null", nil)
			if evidence.ProvenNonNull[nulled.Path+suffix] {
				t.Fatal("an unknown leaf was inferred non-null from its own marker")
			}
		})
	}
}

func TestInvalidFusedIndicesRemainLiteralKeys(t *testing.T) {
	t.Parallel()

	for _, index := range []string{"-1", "-0", "+0", "", "word"} {
		for _, sequence := range []cty.Type{cty.List(cty.String), cty.Tuple([]cty.Type{cty.String})} {
			t.Run(index+"/"+sequence.FriendlyName(), func(t *testing.T) {
				t.Parallel()

				key := "foo[" + index + "]"
				declared := cty.Object(map[string]cty.Type{"foo": sequence, key: cty.String})
				wire, err := ctyjson.MarshalType(declared)
				if err != nil {
					t.Fatal(err)
				}
				for _, prefix := range []string{
					statePrefix + "example_thing.app].values.tags",
					"resource_changes[example_thing.app].change.after.tags",
					regionsStateValue, regionsPlanAfter,
				} {
					resource := "example_thing.app.tags"
					if fingerprint.ParsePath(prefix).Address() == regionsOutput {
						resource = regionsOutput
					}
					expression, err := suggest.Express(schemaTyping("tags", string(wire)),
						suggest.Evidence{OutputTypes: map[string]cty.Type{regionsOutput: declared}},
						changeAt(prefix+"."+key, steadyBaseline))
					assertAdapter(t, expression, err,
						"try("+resource+`["`+key+`"], null) == "steady"`, nil)
				}
			})
		}
	}
}

func TestNestedTupleIndicesRequireUnsignedInRangePositions(t *testing.T) {
	t.Parallel()

	schemas := schemaBlock(nil, map[string]tfexec.SchemaBlockType{
		settingsBlock: {NestingMode: tfexec.NestingSingle, Block: tfexec.SchemaBlock{
			Attributes: map[string]tfexec.SchemaAttribute{
				"items": {Optional: true, Type: json.RawMessage(`["tuple",["string"]]`)},
			},
		}},
	})
	for _, index := range []string{"-1", "-0", "+0", "0", "1", "word", "999999999999999999999999"} {
		for _, prefix := range []string{
			statePrefix + "example_thing.app].values.",
			"resource_changes[example_thing.app].change.after.",
		} {
			t.Run(index+"/"+prefix, func(t *testing.T) {
				t.Parallel()

				want, refusal := "", suggest.ErrUnrenderable
				if index == "0" {
					want, refusal = `try(example_thing.app.settings.items[0], null) == "steady"`, nil
				}
				expression, err := suggest.Express(schemas, suggest.Evidence{},
					changeAt(prefix+"settings.items["+index+"]", steadyBaseline))
				assertAdapter(t, expression, err, want, refusal)
			})
		}
	}
}

func TestNestedUntypedLeavesPreserveScalarFallback(t *testing.T) {
	t.Parallel()

	for _, block := range []struct {
		mode, path, reference string
	}{
		{tfexec.NestingSingle, settingsBlock, settingsBlock},
		{tfexec.NestingGroup, settingsBlock, settingsBlock},
		{tfexec.NestingList, "settings[0]", "settings[0]"},
		{tfexec.NestingMap, "settings.shared", `settings["shared"]`},
	} {
		for _, wire := range []string{`"dynamic"`, "", `"unsupported"`, `"string"`, stringListType} {
			t.Run(block.mode+"/"+wire, func(t *testing.T) {
				t.Parallel()

				schemas := schemaBlock(nil, map[string]tfexec.SchemaBlockType{
					settingsBlock: {NestingMode: block.mode, Block: tfexec.SchemaBlock{
						Attributes: map[string]tfexec.SchemaAttribute{
							"value": {Optional: true, Type: json.RawMessage(wire)},
						},
					}},
				})
				for _, provider := range schemas.ProviderSchemas {
					provider.DataSourceSchemas["example_thing"] = provider.ResourceSchemas["example_thing"]
				}
				assertNestedUntypedLeafFallback(t, schemas, wire, block.path, block.reference)
			})
		}
	}
}

func assertNestedUntypedLeafFallback(t *testing.T, schemas tfexec.Schemas, wire, path, reference string) {
	t.Helper()

	for _, resource := range []string{exampleResource, "data.example_thing.app"} {
		for _, prefix := range []string{
			statePrefix + resource + "].values.",
			"resource_changes[" + resource + "].change.after.",
		} {
			for _, suffix := range []string{"", firstElementIndex, ".child"} {
				expression, err := suggest.Express(schemas, suggest.Evidence{},
					changeAt(prefix+path+".value"+suffix, steadyBaseline))
				want, refusal := "", suggest.ErrUnrenderable
				if (suffix == "" && wire != stringListType) ||
					(suffix == firstElementIndex && wire == stringListType) {
					want = "try(" + resource + "." + reference + ".value" + suffix + `, null) == "steady"`
					refusal = nil
				}
				assertAdapter(t, expression, err, want, refusal)
			}
		}
	}
}
