package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// TestASigintCancelledRunRemovesItsWorkRootAndExitsOperationally holds the
// lifecycle contract the tracer and engine comments promise: the command's
// root context is cancelled by SIGINT, so the run ends as an operational
// failure whose deferred cleanup runs — the work root is removed and the
// spans are flushed — and the process exits instead of dying mid-run.
//
// The child is this test binary again, running the real command line through
// execute, the one place the signal handler is installed. A wrapper Terraform
// sleeps on every test invocation, so the run is parked mid-baseline when the
// signal lands; TMPDIR points the engine's work root at a directory the
// parent can watch, and emptiness after exit is the cleanup proof.
func TestASigintCancelledRunRemovesItsWorkRootAndExitsOperationally(t *testing.T) {
	t.Parallel()

	if os.Getenv(sigintChildEnv) == "1" {
		t.Skip("child process runs the helper instead")
	}

	module := fixture(t)
	workDir := t.TempDir()

	realTerraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("locating terraform: %v", err)
	}

	wrapperDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "baseline-sleeping")
	script := `#!/usr/bin/env bash
if [ "$2" = test ]; then
  echo sleeping >> ` + marker + `
  sleep 300
fi
exec ` + realTerraform + ` "$@"
`

	testsupport.WriteExecutable(t, filepath.Join(wrapperDir, "terraform"), script)

	//nolint:gosec,noctx // re-executing this test binary is the standard helper-process pattern.
	command := exec.Command(os.Args[0],
		"-test.run=TestRunTheCommandLineForSigintHelper", "-test.timeout=120s")
	command.Env = append(os.Environ(),
		sigintChildEnv+"=1",
		"TF_MUT_SIGINT_MODULE="+module,
		"TF_MUT_SIGINT_WORKDIR="+workDir,
		// The engine's work root is created under os.TempDir, which honours
		// TMPDIR: pointing it at the watched directory is how the parent can
		// see the root appear and prove it is removed.
		"TMPDIR="+workDir,
		// The wrapper is found as "terraform" on the child's PATH; it execs
		// the real binary by absolute path, so there is no recursion.
		"PATH="+wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	done := make(chan error, 1)

	if err := command.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}
	registerSigintHelperCleanup(t, command, done)

	go func() {
		done <- command.Wait()
		close(done)
	}()

	waitForWorkRoot(t, workDir)
	waitForMarker(t, marker)

	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("interrupting helper: %v", err)
	}

	select {
	case err := <-done:
		exitErr := &exec.ExitError{}
		if !errors.As(err, &exitErr) {
			t.Fatalf("helper exit: %v", err)
		}

		if code := exitErr.ExitCode(); code != report.ExitOperational {
			t.Fatalf("exit code = %d, want %d (operational)", code, report.ExitOperational)
		}
	case <-time.After(time.Minute):
		t.Fatal("the helper did not exit within a minute of SIGINT")
	}

	entries, readErr := os.ReadDir(workDir)
	if readErr != nil {
		t.Fatalf("reading the work directory: %v", readErr)
	}

	if len(entries) != 0 {
		t.Fatalf("the run's work root outlived the cancelled run: %d entries", len(entries))
	}
}

func registerSigintHelperCleanup(t *testing.T, command *exec.Cmd, done <-chan error) {
	t.Helper()

	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGINT)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the helper was not reaped after SIGKILL")
			}
		}
	})
}

// TestRunTheCommandLineForSigintHelper is the child half of the SIGINT
// reproduction. It only does anything when the parent asks for it.
//
//nolint:paralleltest // the helper must own the process and its exit code.
func TestRunTheCommandLineForSigintHelper(t *testing.T) {
	if os.Getenv(sigintChildEnv) != "1" {
		t.Skip("helper process only")
	}

	// No t.Parallel, no t.Context: the signal handler inside execute must be
	// the thing that ends this process, and os.Exit publishes its verdict.
	devnull := io.Discard
	code := execute(runCommand, "dev",
		[]string{"--jobs", "1", os.Getenv("TF_MUT_SIGINT_MODULE")}, devnull, devnull)

	// The helper half of the reproduction ends itself: its exit code is the
	// observation.
	//revive:disable-next-line:deep-exit
	os.Exit(code)
}

// sigintChildEnv marks the helper half of the SIGINT reproduction.
const sigintChildEnv = "TF_MUT_SIGINT_CHILD"

// waitForMarker polls until the wrapper has parked the baseline run, so the
// signal lands mid-run rather than during start-up.
func waitForMarker(t *testing.T, marker string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}

		if time.Now().After(deadline) {
			t.Fatal("the helper never reached the baseline test run")
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// waitForWorkRoot polls until the engine has created its run directory, so
// the signal lands mid-run rather than during start-up.
func waitForWorkRoot(t *testing.T, workDir string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		entries, err := os.ReadDir(workDir)
		if err == nil && len(entries) > 0 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatal("the helper never created a work root")
		}

		time.Sleep(20 * time.Millisecond)
	}
}
