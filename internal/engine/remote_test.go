package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestRemoteModuleIsPresentForExecutionButNeverMutated is the R2-7
// reproduction. The remote source is a git repository on the local filesystem,
// which is remote as far as Terraform is concerned — the payload is installed
// under .terraform/modules and must be shared into every sandbox — while
// keeping the test offline.
func TestRemoteModuleIsPresentForExecutionButNeverMutated(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to build the remote-module fixture")
	}

	origin := gitOrigin(t)
	module := remoteConsumer(t, origin)
	before := treeDigest(t, module)
	originBefore := treeDigest(t, origin)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated for the consuming root module")
	}

	for _, mutant := range result.Mutants {
		if strings.Contains(mutant.Range.File, ".terraform") {
			t.Fatalf("a remote module was mutated: %s", mutant.Range.File)
		}
	}

	if got := stateOf(t, result, "output.name"); got != report.Killed {
		t.Fatalf("the remote module's output = %s, want %s; the payload must be "+
			"present for execution", got, report.Killed)
	}

	assertTreeUnchanged(t, module, before)
	assertTreeUnchanged(t, origin, originBefore)
}

// gitOrigin creates a one-commit repository containing a child module.
func gitOrigin(t *testing.T) string {
	t.Helper()

	origin := filepath.Join(t.TempDir(), "origin")
	child := filepath.Join(origin, "mod")

	if err := os.MkdirAll(child, 0o750); err != nil {
		t.Fatalf("creating origin: %v", err)
	}

	writeFile(t, filepath.Join(child, "main.tf"), `variable "prefix" {
  type    = string
  default = "remote"
}

output "name" {
  value = "${var.prefix}-module"
}
`)

	git(t, origin, "init", "--quiet")
	git(t, origin, "add", ".")
	git(t, origin, "-c", "user.email=tests@example.invalid", "-c", "user.name=tf-mut tests",
		"commit", "--quiet", "-m", "child module")

	return origin
}

// remoteConsumer creates a root module whose only child is the git source.
func remoteConsumer(t *testing.T, origin string) string {
	t.Helper()

	module := filepath.Join(t.TempDir(), "consumer")
	if err := os.MkdirAll(filepath.Join(module, "tests"), 0o750); err != nil {
		t.Fatalf("creating consumer: %v", err)
	}

	writeFile(t, filepath.Join(module, "main.tf"), `module "remote" {
  source = "git::file://`+origin+`//mod"
  prefix = "root"
}

output "name" {
  value = module.remote.name
}
`)

	writeFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), `run "wires_the_remote_module" {
  command = plan

  assert {
    condition     = output.name == "root-module"
    error_message = "the remote module must resolve inside the sandbox"
  }
}
`)

	return module
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	//nolint:gosec // every argument is a literal or a test-owned temporary path.
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
