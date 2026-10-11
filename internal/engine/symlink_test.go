package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// symlinkedModule builds a module directory holding one symlinked .tf file
// whose relative target lies outside the module directory — a repository
// layout where a module shares one versions.tf with its neighbours by linking
// to it. The target's variable is what the suite asserts on, so a sandbox
// that loses the file cannot score.
func symlinkedModule(t *testing.T) string {
	t.Helper()

	repository := t.TempDir()
	module := filepath.Join(repository, "mod")
	if err := os.MkdirAll(filepath.Join(module, "tests"), 0o750); err != nil {
		t.Fatalf("creating module: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "shared"), 0o750); err != nil {
		t.Fatalf("creating shared directory: %v", err)
	}

	writeFile(t, filepath.Join(repository, "shared", "versions.tf"), `variable "shared_tag" {
  type    = string
  default = "shared-v1"
}
`)

	if err := os.Symlink("../shared/versions.tf", filepath.Join(module, "versions.tf")); err != nil {
		t.Fatalf("linking versions.tf: %v", err)
	}

	writeFile(t, filepath.Join(module, "main.tf"), `output "tag" {
  value = upper(var.shared_tag)
}
`)

	writeFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), `run "tag" {
  command = plan

  assert {
    condition     = output.tag == "SHARED-V1"
    error_message = "the symlinked variable must resolve inside the sandbox"
  }
}
`)

	return module
}

// TestASymlinkedSourceResolvesInTheSandbox: a sandbox that shares an
// unmutated symlinked .tf file by linking the link hands Terraform a dangling
// path — the link's relative target names a neighbourhood of the source tree
// that does not exist inside the sandbox — so Terraform refuses every mutant
// it cannot rewrite with "Failed to read file". The sandbox must materialise
// the link's target bytes under the link's own name instead, exactly as the
// warm workspace and the input digest already read them.
func TestASymlinkedSourceResolvesInTheSandbox(t *testing.T) {
	t.Parallel()

	module := symlinkedModule(t)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated for the symlinked module")
	}

	for _, mutant := range result.Mutants {
		for _, diagnostic := range mutant.Diagnostics {
			if strings.Contains(diagnostic.Detail, "could not be read") {
				t.Fatalf("mutant %s at %s was refused with %q; the sandbox left the "+
					"symlinked source dangling", mutant.ID, mutant.Site, diagnostic.Detail)
			}
		}

		if mutant.State == report.Invalid {
			t.Fatalf("mutant %s at %s was Invalid; the sandbox left the symlinked "+
				"source dangling", mutant.ID, mutant.Site)
		}
	}

	if got := stateOf(t, result, "output.tag"); got != report.Killed {
		t.Fatalf("output.tag = %s, want %s; the symlinked variable must be scored",
			got, report.Killed)
	}
}
