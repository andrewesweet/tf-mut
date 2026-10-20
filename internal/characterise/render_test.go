package characterise_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
)

// The roots the bare and path rows share, named once: each root's bytes are
// the pin, and the pair of rows proves the path extension is truncated
// rather than re-derived.
const (
	pinnedVariableRoot = "var.size"
	pinnedOutputRoot   = "output.both"
	pinnedResourceRoot = "terraform_data.second"
)

func TestCheckablePreservesRootTruncation(t *testing.T) {
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
		"data source, precondition":     {"data.http.flag.lifecycle.precondition", "data.http", true},
		"local value":                   {"local.mode", "local.mode", true},
		"resource inside a module call": {"module.child.aws_instance.web.user_data", "module.child", true},
		"indexed resource":              {"terraform_data.second[0].input", "terraform_data.second[0]", true},
		"key containing a dot":          {`terraform_data.second["a.b"].input`, `terraform_data.second["a`, true},
		"empty second segment":          {"var..validation", "var.", true},
		"empty first segment":           {".size.validation", ".size", true},
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
