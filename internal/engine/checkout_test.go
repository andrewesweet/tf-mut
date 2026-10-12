package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
)

// The checkout boundary: a run block whose local module source resolves
// outside the git work tree is refused with a message that names the test
// file, the run block and the source string — not with the raw read error
// of the directory the discovery went on to open.

func TestARunBlockSourceLeavingTheCheckoutIsRefusedWithTheRunNamed(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "runblock-outside")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrParse) {
		t.Fatal("a run block whose source leaves the checkout must be refused as a discovery failure")
	}

	for _, fragment := range []string{
		"leaves the checkout",
		unitTestFile,
		`run "outside"`,
		`"../outside-sibling"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}
}
