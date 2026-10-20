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

func TestTypedMemberKeysPreserveTheirLiteralSpelling(t *testing.T) {
	t.Parallel()

	for _, key := range []struct{ value, quoted string }{
		{"cost$", `"cost$"`},
		{"tax%", `"tax%"`},
		{"${part}%{blk}", `"$${part}%%{blk}"`},
		{"a\b\f\v\ab", `"a\u0008\u000c\u000b\u0007b"`},
		{"[prod]", `"[prod]"`},
		{"[0]", `"[0]"`},
	} {
		for _, declared := range []cty.Type{cty.Map(cty.String), cty.Object(map[string]cty.Type{key.value: cty.String})} {
			t.Run(key.quoted+"/"+declared.FriendlyName(), func(t *testing.T) {
				t.Parallel()

				wire, err := ctyjson.MarshalType(declared)
				if err != nil {
					t.Fatal(err)
				}

				for _, prefix := range []string{statePrefix + "example_thing.app].values.tags", "resource_changes[example_thing.app].change.after.tags", "outputs.regions.value", "output_changes.regions.after"} {
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
					Attributes: map[string]tfexec.SchemaAttribute{"source": {Optional: true, Type: json.RawMessage(`"string"`)}},
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
		{"empty", "[]", ""},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()

			leaf := cty.String
			if form.name == "empty" {
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

			for _, prefix := range []string{statePrefix + "example_thing.app].values.tags", "outputs.regions.value", "output_changes.regions.after"} {
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

	for _, prefix := range []string{"outputs.regions.value", "output_changes.regions.after"} {
		for _, suffix := range []string{".member", "[0]"} {
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
	for _, suffix := range []string{".member", "[0]"} {
		t.Run("unknown"+suffix, func(t *testing.T) {
			t.Parallel()

			nulled := nulledAt("output_changes.regions.after")
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
