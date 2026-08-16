package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

const (
	oversubscribedJobs = 8
	crashPollInterval  = 20 * time.Millisecond
	crashPollLimit     = 200
)

func TestConcurrentReadersOfTheSharedProviderTreeDoNotInterfere(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "mocked-null")

	config := baseConfig(t, module)
	config.Jobs = oversubscribedJobs

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) != 0 {
		t.Fatalf("concurrent provider readers produced operational failures: %v", result.Errors)
	}

	if result.Count(report.Invalid) != 0 {
		t.Fatalf("concurrent provider readers produced invalid mutants: %v", result.Metrics.Counts)
	}

	for _, mutant := range result.Mutants {
		if mutant.ExecutedRuns == 0 {
			t.Fatalf("mutant %s executed no runs under concurrency", mutant.ID)
		}
	}
}

func TestOneFailingWorkerDoesNotPoisonTheOthers(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	// A Terraform that dies for exactly one mutant's sandbox, which is the
	// observable shape of a worker crash, and which is deterministic at any
	// parallelism because it keys on the sandbox path rather than on order.
	config.TerraformBinary = failingTerraform(t, "m0001-")

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly one operational failure, got %d: %v", len(result.Errors), result.Errors)
	}

	verdicts := 0

	for _, mutant := range result.Mutants {
		if mutant.State == report.Killed || mutant.State == report.Survived {
			verdicts++
		}
	}

	if verdicts == 0 {
		t.Fatalf("the surviving workers produced no verdicts: %v", result.Metrics.Counts)
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitOperational { //nolint:exhaustruct // no gate.
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}
}

// failingTerraform wraps the real binary and makes it die without output for
// the one sandbox whose path contains the marker, the way a killed worker
// would.
func failingTerraform(t *testing.T, marker string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "terraform-flaky")

	script := `#!/usr/bin/env bash
if [ "$2" = "test" ]; then
  case "$1" in
    *` + marker + `*) exit 137 ;;
  esac
fi
exec terraform "$@"
`

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the wrapper must run.
		t.Fatalf("writing terraform wrapper: %v", err)
	}

	return path
}

// TestKillingTheProcessLeavesTheSourceTreeIntact is the R2-3 reproduction in
// its harshest form: the process is killed outright, so no cleanup runs.
func TestKillingTheProcessLeavesTheSourceTreeIntact(t *testing.T) {
	t.Parallel()

	if os.Getenv("TF_MUT_TEST_CHILD") == "1" {
		t.Skip("child process runs the helper instead")
	}

	module := copyFixture(t, "skeleton")
	before := treeDigest(t, module)
	workDir := t.TempDir()

	//nolint:gosec // re-executing this test binary is the standard helper-process pattern.
	command := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=TestRunEngineForCrashHelper", "-test.timeout=120s")
	command.Env = append(os.Environ(),
		"TF_MUT_TEST_CHILD=1",
		"TF_MUT_TEST_MODULE="+module,
		"TF_MUT_TEST_WORKDIR="+workDir,
	)

	if err := command.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}

	waitForSandbox(t, workDir)

	if err := command.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing helper: %v", err)
	}

	_ = command.Wait()

	assertTreeUnchanged(t, module, before)
}

// TestRunEngineForCrashHelper is the child half of the crash reproduction. It
// only does anything when the parent asks for it.
func TestRunEngineForCrashHelper(t *testing.T) {
	t.Parallel()

	if os.Getenv("TF_MUT_TEST_CHILD") != "1" {
		t.Skip("helper process only")
	}

	config := baseConfig(t, os.Getenv("TF_MUT_TEST_MODULE"))
	config.WorkDir = os.Getenv("TF_MUT_TEST_WORKDIR")
	config.Jobs = 1

	for range crashPollLimit {
		if _, err := engine.Run(t.Context(), config); err != nil {
			t.Fatalf("helper run: %v", err)
		}
	}
}

func waitForSandbox(t *testing.T, workDir string) {
	t.Helper()

	for range crashPollLimit {
		entries, err := os.ReadDir(workDir)
		if err == nil && len(entries) > 0 {
			return
		}

		time.Sleep(crashPollInterval)
	}

	t.Fatal("the helper never created a working directory")
}
