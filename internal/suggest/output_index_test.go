package suggest_test

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/suggest"
)

const emptyListBaseline = "[]"

func TestOutputIndicesRequireBaselineListOrTupleEvidence(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, attribute, baseline, mutant, want string
		declared                                cty.Type
	}{
		{"missing type", "[0]", steadyBaseline, "", "", cty.NilType},
		{"set scalar", "[0]", steadyBaseline, "", "", cty.Set(cty.String)},
		{"set null", "[0]", steadyBaseline, nulledValue, "", cty.Set(cty.String)},
		{"set emptiness", "[0]", emptyListBaseline, "", "", cty.Set(cty.List(cty.String))},
		{"list scalar", "[0]", steadyBaseline, "", `output.regions[0] == "steady"`, cty.List(cty.String)},
		{"tuple scalar", "[0]", steadyBaseline, "", `output.regions[0] == "steady"`, cty.Tuple([]cty.Type{cty.String})},
		{"list null", "[0]", steadyBaseline, nulledValue, "output.regions[0] != null", cty.List(cty.String)},
		{"list emptiness", "[0]", emptyListBaseline, "", "length(output.regions[0]) == 0", cty.List(cty.List(cty.String))},
		{"tuple out of range", "[1]", steadyBaseline, "", "", cty.Tuple([]cty.Type{cty.String})},
		{
			"nested set", "[0].aliases[0]", steadyBaseline, "", "",
			cty.List(cty.Object(map[string]cty.Type{"aliases": cty.Set(cty.String)})),
		},
		{
			"member set null", ".aliases[0]", steadyBaseline, nulledValue, "",
			cty.Object(map[string]cty.Type{"aliases": cty.Set(cty.String)}),
		},
		{
			"member set emptiness", ".aliases[0]", emptyListBaseline, "", "",
			cty.Object(map[string]cty.Type{"aliases": cty.Set(cty.List(cty.String))}),
		},
		{
			"member list", ".aliases[0]", steadyBaseline, "", `output.regions.aliases[0] == "steady"`,
			cty.Object(map[string]cty.Type{"aliases": cty.List(cty.String)}),
		},
	} {
		for _, prefix := range []string{"outputs.regions.value", "output_changes.regions.after"} {
			t.Run(testCase.name+"/"+prefix, func(t *testing.T) {
				t.Parallel()

				change := changeAt(prefix+testCase.attribute, testCase.baseline)
				change.Mutant = testCase.mutant
				expression, err := suggest.Express(runIn("applied", ""), untyped(),
					suggest.Evidence{OutputTypes: map[string]cty.Type{"output.regions": testCase.declared}}, change)
				var refused error
				if testCase.want == "" {
					refused = suggest.ErrUnrenderable
				}
				assertAdapter(t, expression, err, testCase.want, refused)
			})
		}
	}
}
