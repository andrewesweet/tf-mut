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

// The same boundary for an ordinary module call: a local source that leaves
// the checkout is refused with the declaring file, the call and the resolved
// path named, exactly as a run-block retarget is — otherwise the closure
// follows a directory the sandbox copy can never contain and the run fails
// later and less clearly.

const (
	escapesMessage = "leaves the checkout"
	escapingSource = `"../outside-sibling"`
)

func TestAModuleCallSourceLeavingTheCheckoutIsRefusedWithTheCallNamed(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "modulecall-outside")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrParse) {
		t.Fatal("a module call whose source leaves the checkout must be refused as a discovery failure")
	}

	for _, fragment := range []string{
		escapesMessage,
		mainFile,
		`module "outside"`,
		escapingSource,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}
}

// The JSON-declared variant: the call lives in a `.tf.json` file, and the
// refusal rides on the same JSON inventory the closure itself follows.

func TestAJSONModuleCallSourceLeavingTheCheckoutIsRefusedWithTheCallNamed(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "modulecall-outside-json")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrParse) {
		t.Fatal("a JSON-declared module call whose source leaves the checkout must be refused as a discovery failure")
	}

	for _, fragment := range []string{
		escapesMessage,
		"calls.tf.json",
		`module "outside"`,
		escapingSource,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}
}

// A call whose source stays inside the checkout is followed, not refused: the
// child joins the closure and its own mutants are generated.

func TestAModuleCallInsideTheCheckoutIsAccepted(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "modulecall-inside")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("a module call inside the checkout must be accepted: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.Module == "child" {
			return
		}
	}

	t.Fatal("the child module the call named never entered the closure; its mutants are missing")
}

func TestARegistryModuleSourceInsideACheckoutIsAccepted(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "modulecall-registry")

	_, err := engine.Run(t.Context(), todosRequest(t, module))
	if err != nil {
		t.Fatalf("a registry module source inside the checkout must be accepted: %v", err)
	}
}

// When Git is unavailable the engine passes no checkout root, and the
// boundary is explicitly skipped: the closure then follows the missing
// directory and the run fails with a raw read error — never the checkout
// refusal.

func TestWithoutACheckoutRootTheBoundaryIsSkippedNotRefused(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "modulecall-outside")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if err == nil {
		t.Fatal("the run must still fail: the directory the call names does not exist")
	}

	if errors.Is(err, discovery.ErrParse) {
		t.Fatalf("without a checkout root the boundary is skipped, yet a discovery refusal fired: %v", err)
	}

	if strings.Contains(err.Error(), escapesMessage) {
		t.Fatalf("without a checkout root the refusal must not fire: %v", err)
	}

	if !strings.Contains(err.Error(), "outside-sibling") {
		t.Fatalf("the failure must be the closure following the missing directory, got: %v", err)
	}
}

func TestARunBlockSourceLeavingTheCheckoutIsRefusedWithTheRunNamed(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "runblock-outside")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrParse) {
		t.Fatal("a run block whose source leaves the checkout must be refused as a discovery failure")
	}

	for _, fragment := range []string{
		escapesMessage,
		unitTestFile,
		`run "outside"`,
		escapingSource,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}
}
