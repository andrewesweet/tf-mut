package characterise

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// The synthesis evaluator's function table is a contract about *expressions*,
// not about Terraform behaviour, and it is tested here directly for the reason
// AGENTS.md's first recorded exception gives: driving the real binary into
// producing each of eleven function semantics on demand is not possible, and
// every verdict the table leads to is still asserted through the engine seam.
//
// It earns its own exception because the table is where a wrong answer is
// silent. A function this evaluator implements incorrectly does not fail — it
// accepts a value the module rejects, or refuses one the module accepts, and
// the only symptom is a judgement point that should not exist or a scenario
// Terraform declines to plan.

// evaluateCondition binds a value and evaluates a condition through the same
// table synthesis uses.
func evaluateCondition(t *testing.T, condition, value string) (holds, decidable bool) {
	t.Helper()

	expr, diagnostics := hclsyntax.ParseExpression([]byte(condition), "test", hcl.InitialPos)
	if diagnostics.HasErrors() {
		t.Fatalf("parsing %q: %s", condition, diagnostics.Error())
	}

	bound, diagnostics := hclsyntax.ParseExpression([]byte(value), "test", hcl.InitialPos)
	if diagnostics.HasErrors() {
		t.Fatalf("parsing %q: %s", value, diagnostics.Error())
	}

	given, diagnostics := bound.Value(nil)
	if diagnostics.HasErrors() {
		t.Fatalf("evaluating %q: %s", value, diagnostics.Error())
	}

	return evaluate(
		discovery.Validation{Condition: expr, File: "test", Range: expr.Range()},
		&hcl.EvalContext{
			Variables: map[string]cty.Value{
				variableRoot: cty.ObjectVal(map[string]cty.Value{"x": given}),
			},
			Functions: validationFunctionTable,
		},
	)
}

func TestTheValidationFunctionTable(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		condition string
		value     string
		holds     bool
		decidable bool
	}{
		"contains, member":      {`contains(["a", "b"], var.x)`, `"a"`, true, true},
		"contains, absent":      {`contains(["a", "b"], var.x)`, `"z"`, false, true},
		"length, over":          {`length(var.x) > 2`, `"abc"`, true, true},
		"length, under":         {`length(var.x) > 2`, `"ab"`, false, true},
		"regex through can":     {`can(regex("^ami-[0-9a-f]+$", var.x))`, `"ami-0123ab"`, true, true},
		"regex refuted by can":  {`can(regex("^ami-[0-9a-f]+$", var.x))`, `"not-an-ami"`, false, false},
		"regexall, matches":     {`length(regexall("a", var.x)) > 1`, `"banana"`, true, true},
		"lower":                 {`lower(var.x) == "abc"`, `"ABC"`, true, true},
		"upper":                 {`upper(var.x) == "ABC"`, `"abc"`, true, true},
		"startswith, yes":       {`startswith(var.x, "tf-")`, `"tf-mut"`, true, true},
		"startswith, no":        {`startswith(var.x, "tf-")`, `"mut"`, false, true},
		"endswith, yes":         {`endswith(var.x, ".tf")`, `"main.tf"`, true, true},
		"endswith, no":          {`endswith(var.x, ".tf")`, `"main.json"`, false, true},
		"alltrue, all":          {`alltrue([for v in var.x : v != ""])`, `["a", "b"]`, true, true},
		"alltrue, one empty":    {`alltrue([for v in var.x : v != ""])`, `["a", ""]`, false, true},
		"anytrue, one":          {`anytrue([for v in var.x : v == "a"])`, `["z", "a"]`, true, true},
		"anytrue, none":         {`anytrue([for v in var.x : v == "a"])`, `["y", "z"]`, false, true},
		"an unimplemented call": {`can(cidrnetmask(var.x))`, `"10.0.0.0/16"`, false, false},
	}

	for name, expected := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			holds, decidable := evaluateCondition(t, expected.condition, expected.value)
			if holds != expected.holds || decidable != expected.decidable {
				t.Fatalf("%s over %s = (holds %v, decidable %v), want (%v, %v)",
					expected.condition, expected.value,
					holds, decidable, expected.holds, expected.decidable)
			}
		})
	}
}
