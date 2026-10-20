package suggest_test

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/suggest"
)

const (
	emptyListBaseline   = "[]"
	aliasesFirstElement = ".aliases[0]"
)

// TestTypedOutputWalksRenderWhatTheBaselineTypeProves holds the reworked
// output walk at the shapes its type evidence admits and refuses. Every row
// publishes the output type the baseline carries, and the walk must render
// exactly the readings that type justifies — a fused member[index] step
// through a list of objects, a literal key spelled like an index, a map under
// a list — and refuse the reading the type cannot decide between. Where the
// form is the null or emptiness one the terminal may be a collection, which
// no scalar equality could express.
func TestTypedOutputWalksRenderWhatTheBaselineTypeProves(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, attribute, baseline, mutant, want string
		declared                                cty.Type
	}{
		{
			// N3: the fused name[index] step through a list of objects.
			name:      "a deeply fused object walk renders from the type",
			attribute: "[0].inner.items[1]", baseline: `"n"`,
			declared: cty.List(cty.Object(map[string]cty.Type{
				"inner": cty.Object(map[string]cty.Type{
					"items": cty.List(cty.String),
				}),
			})),
			want: `try(output.regions[0].inner.items[1], null) == "n"`,
		},
		{
			// R13: a map under a list — the whole-segment reading names a key
			// whose value is a list, which no scalar equality can take, so the
			// type evidence alone picks the indexed reading. The key stays
			// dotted, a valid identifier the attribute syntax reads.
			name:      "a list of maps renders the indexed map entry",
			attribute: "[0].k[1]", baseline: `"s"`,
			declared: cty.List(cty.Map(cty.List(cty.String))),
			want:     `try(output.regions[0].k[1], null) == "s"`,
		},
		{
			// R12, both halves: the list attribute and the literal key spelled
			// like its first index are indistinguishable in the payload, and
			// the type cannot decide between them, so nothing renders.
			name:      "a literal key and a list index the type cannot tell apart are refused",
			attribute: ".aliases[0]", baseline: `"v"`,
			declared: cty.Object(map[string]cty.Type{
				aliasesAttribute: cty.List(cty.String),
				"aliases[0]":     cty.String,
			}),
			want: "",
		},
		{
			// R12, literal only: the type proves the key is the attribute.
			name:      "a literal key spelled like an index renders in index form",
			attribute: ".aliases[0]", baseline: `"v"`,
			declared: cty.Object(map[string]cty.Type{
				"aliases[0]": cty.String,
			}),
			want: `try(output.regions["aliases[0]"], null) == "v"`,
		},
		{
			// N4: the null form over a typed collection terminal.
			name:      "a nulled typed collection element asserts non-null",
			attribute: "[0].sub", baseline: `["a"]`, mutant: nulledValue,
			declared: cty.List(cty.Object(map[string]cty.Type{
				"sub": cty.List(cty.String),
			})),
			want: `try(output.regions[0].sub, null) != null`,
		},
		{
			// N4: the emptiness form over the same terminal.
			name:      "an emptied typed collection element is a length",
			attribute: "[0].sub", baseline: "[]",
			declared: cty.List(cty.Object(map[string]cty.Type{
				"sub": cty.List(cty.String),
			})),
			want: "try(length(output.regions[0].sub), null) == 0",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			change := changeAt("outputs.regions.value"+testCase.attribute, testCase.baseline)
			change.Mutant = testCase.mutant

			expression, err := suggest.Express(untyped(),
				suggest.Evidence{OutputTypes: map[string]cty.Type{regionsOutput: testCase.declared}}, change)

			var refused error
			if testCase.want == "" {
				refused = suggest.ErrUnrenderable
			}
			assertAdapter(t, expression, err, testCase.want, refused)
		})
	}
}

func TestOutputIndicesRequireBaselineListOrTupleEvidence(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, attribute, baseline, mutant, want string
		declared                                cty.Type
	}{
		{"missing type", firstElementIndex, steadyBaseline, "", "", cty.NilType},
		{"set scalar", firstElementIndex, steadyBaseline, "", "", cty.Set(cty.String)},
		{"set null", firstElementIndex, steadyBaseline, nulledValue, "", cty.Set(cty.String)},
		{"set emptiness", firstElementIndex, emptyListBaseline, "", "", cty.Set(cty.List(cty.String))},
		{
			"list scalar", firstElementIndex, steadyBaseline, "", `try(output.regions[0], null) == "steady"`,
			cty.List(cty.String),
		},
		{
			"tuple scalar", firstElementIndex, steadyBaseline, "", `try(output.regions[0], null) == "steady"`,
			cty.Tuple([]cty.Type{cty.String}),
		},
		{
			"list null", firstElementIndex, steadyBaseline, nulledValue, "try(output.regions[0], null) != null",
			cty.List(cty.String),
		},
		{
			"list emptiness", firstElementIndex, emptyListBaseline, "", "try(length(output.regions[0]), null) == 0",
			cty.List(cty.List(cty.String)),
		},
		{"tuple out of range", "[1]", steadyBaseline, "", "", cty.Tuple([]cty.Type{cty.String})},
		{
			"nested set", "[0].aliases[0]", steadyBaseline, "", "",
			cty.List(cty.Object(map[string]cty.Type{aliasesAttribute: cty.Set(cty.String)})),
		},
		{
			"member set null", aliasesFirstElement, steadyBaseline, nulledValue, "",
			cty.Object(map[string]cty.Type{aliasesAttribute: cty.Set(cty.String)}),
		},
		{
			"member set emptiness", aliasesFirstElement, emptyListBaseline, "", "",
			cty.Object(map[string]cty.Type{aliasesAttribute: cty.Set(cty.List(cty.String))}),
		},
		{
			"member list", aliasesFirstElement, steadyBaseline, "", `try(output.regions.aliases[0], null) == "steady"`,
			cty.Object(map[string]cty.Type{aliasesAttribute: cty.List(cty.String)}),
		},
	} {
		for _, prefix := range []string{"outputs.regions.value", "output_changes.regions.after"} {
			t.Run(testCase.name+"/"+prefix, func(t *testing.T) {
				t.Parallel()

				change := changeAt(prefix+testCase.attribute, testCase.baseline)
				change.Mutant = testCase.mutant
				expression, err := suggest.Express(untyped(),
					suggest.Evidence{OutputTypes: map[string]cty.Type{regionsOutput: testCase.declared}}, change)
				var refused error
				if testCase.want == "" {
					refused = suggest.ErrUnrenderable
				}
				assertAdapter(t, expression, err, testCase.want, refused)
			})
		}
	}
}
