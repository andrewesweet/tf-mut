package characterise_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
)

// Checkable is a contract about address documents — the roots an
// expect_failures entry can name — not about Terraform behaviour, and it is
// tested here directly for the reason AGENTS.md's first recorded exception
// gives: driving the real binary into producing each address shape on demand
// is not possible. Every verdict the roots lead to is still asserted through
// the engine seam: the promotion path stages and executes the rendered file
// before a scaffold becomes test content.
//
// The table began as the characterisation pin for the root rendering: before
// the rendering moved onto the shared discovery block-address code, every row
// below held against the old two-segment split, so the rows that still hold
// are the byte-identity contract, and any row that changes is an intended,
// reviewed behaviour delta — never a side effect.
//
// Three rows changed in that move, each verified against Terraform v1.15.8
// itself (research/spikes is the standing rule: never trust a plausible
// reading). A data source's root is now the full `data.<type>.<name>` — the
// old split rendered `data.<type>`, which Terraform statically refuses ("The
// `data` object must be followed by two attribute names"). A module-relative
// resource renders its resource root through the module path rather than the
// bare module call. And a locals value is refused: Terraform rejects
// `local.<name>` as an expect_failures target, so the shared code's refusal
// surfaces at promotion as "names no object expect_failures can accept"
// instead of a generated file Terraform would reject.

// The roots the bare and path rows share, named once: each root's bytes are
// the pin, and the pair of rows proves the path extension is truncated
// rather than re-derived.
const (
	pinnedVariableRoot = "var.size"
	pinnedOutputRoot   = "output.both"
	pinnedResourceRoot = "terraform_data.second"
)

func TestCheckableRendersTheRootExpectFailuresNames(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		site      string
		checkable string
		addressed bool
	}{
		"variable, validation path":     {"var.size.validation.condition", pinnedVariableRoot, true},
		"variable, bare":                {pinnedVariableRoot, pinnedVariableRoot, true},
		"output, attribute path":        {"output.both.value", pinnedOutputRoot, true},
		"output, bare":                  {pinnedOutputRoot, pinnedOutputRoot, true},
		"resource, lifecycle path":      {"terraform_data.second.lifecycle.precondition", pinnedResourceRoot, true},
		"resource, bare":                {pinnedResourceRoot, pinnedResourceRoot, true},
		"check, assertion path":         {"check.story.assert.claim", "check.story", true},
		"data source, precondition":     {"data.http.flag.lifecycle.precondition", "data.http.flag", true},
		"local value is refused":        {"local.mode", "", false},
		"resource inside a module call": {"module.child.aws_instance.web.user_data", "module.child.aws_instance.web", true},
		"one segment only":              {"size", "", false},
		"empty address":                 {"", "", false},
	}

	for name, expected := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkable, addressed := characterise.Checkable(expected.site)
			if checkable != expected.checkable || addressed != expected.addressed {
				t.Fatalf("Checkable(%q) = (%q, %v), want (%q, %v)",
					expected.site, checkable, addressed, expected.checkable, expected.addressed)
			}
		})
	}
}
