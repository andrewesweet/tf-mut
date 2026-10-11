package suggest_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// The verified-assertion message and its inverse are one contract about
// documents: verification attributes a failing batch through the identifiers
// the failing runs' diagnostics carry, so the parser must recognise exactly
// what the renderer writes and nothing else. Driving the real binary into
// producing each shape on demand is not possible; every verdict the parser
// leads to is asserted through the engine seam.
func TestSuggestionIDFromMessage(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		message string
		wantID  string
		wantOK  bool
	}{
		"round-trips the rendered message": {
			message: suggest.VerifiedMessage("aaaabbbbcccc", "ddddeeeeffff"),
			wantID:  "aaaabbbbcccc",
			wantOK:  true,
		},
		"refuses ordinary diagnostic text": {
			message: "Invalid index: The given key does not identify an element in this collection value.",
			wantID:  "",
			wantOK:  false,
		},
		"refuses the phrase without an identifier": {
			message: "tf-mut suggestion  catches mutant ddddeeeeffff",
			wantID:  "",
			wantOK:  false,
		},
		"refuses a truncated message": {
			message: "tf-mut suggestion aaaabbbbcccc",
			wantID:  "",
			wantOK:  false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			id, ok := suggest.SuggestionIDFromMessage(test.message)
			if ok != test.wantOK || id != test.wantID {
				t.Fatalf("SuggestionIDFromMessage(%q) = %q, %v, want %q, %v",
					test.message, id, ok, test.wantID, test.wantOK)
			}
		})
	}
}
