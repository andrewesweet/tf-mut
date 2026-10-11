package characterise_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
)

// TestAnExpressionAddressJoinsTheWayAssertionsSpell pins the address join
// against HCL's own grammar: an attribute path that opens with an index
// continues from the address directly, because `output.x.[0]` parses as two
// traversals where the assertion needs one. The shapes are documents'
// contracts — the engine fixtures pin every verdict the join leads to, and
// the split it mirrors is the fingerprint package's own grammar.
func TestAnExpressionAddressJoinsTheWayAssertionsSpell(t *testing.T) {
	t.Parallel()

	const listOutput = "output.region_list"

	for _, testCase := range []struct {
		address, attribute, want string
	}{
		{listOutput, "", listOutput},
		{listOutput, "region", listOutput + ".region"},
		{listOutput, "[0].short_name", listOutput + "[0].short_name"},
		{"terraform_data.counted", "[1]", "terraform_data.counted[1]"},
	} {
		if got := characterise.ExpressionAddress(testCase.address, testCase.attribute); got != testCase.want {
			t.Fatalf("ExpressionAddress(%q, %q) = %q, want %q",
				testCase.address, testCase.attribute, got, testCase.want)
		}
	}
}
