package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// D6: cancellation after an apply rename suppressed the report of the files
// already changed. A verified assertion is on disk, and the command exits with
// only "tf-mut: context canceled" — no standard output, no requested JSON
// receipt — so the caller cannot tell which requested writes landed.
//
// The reproduction is the reported one: the real command line, the real
// Terraform binary on an offline fixture, and a SIGINT sent the instant the
// test file's bytes change underneath the run — after the atomic rename, before
// the report is published. The control case is the same command with no signal:
// it exits 0 and produces the receipt, which is what the interrupted case owes
// the caller.

const (
	// applyCancelChildEnv marks the helper half of the reproduction.
	applyCancelChildEnv = "TF_MUT_APPLY_CANCEL_CHILD"
	// The helper's inputs and outputs, passed through the environment because
	// the helper is this test binary re-executed.
	applyCancelModuleEnv  = "TF_MUT_APPLY_CANCEL_MODULE"
	applyCancelReceiptEnv = "TF_MUT_APPLY_CANCEL_RECEIPT"
	applyCancelStdoutEnv  = "TF_MUT_APPLY_CANCEL_STDOUT"
	applyCancelStderrEnv  = "TF_MUT_APPLY_CANCEL_STDERR"
	applyCancelProfileEnv = "TF_MUT_APPLY_CANCEL_PROFILE"
	applyCancelCodeEnv    = "TF_MUT_APPLY_CANCEL_CODE"

	// applyCancelFixture is the offline module whose unasserted resource yields
	// one verifiable suggestion, and applyCancelTarget is the file it lands in.
	applyCancelFixture = "../../internal/engine/testdata/suggest-basic"
	applyCancelTarget  = "tests/unit.tftest.hcl"
)

// TestASigintAfterAnApplyRenameStillReportsTheCommittedWrite holds the write
// receipt across cancellation: an invocation that committed a file before the
// signal must report that file, and must report it as a receipt of committed
// effects rather than as a completed score.
func TestASigintAfterAnApplyRenameStillReportsTheCommittedWrite(t *testing.T) {
	t.Parallel()

	if os.Getenv(applyCancelChildEnv) == "1" {
		t.Skip("child process runs the helper instead")
	}

	module := filepath.Join(t.TempDir(), "suggest-basic")
	if err := os.CopyFS(module, os.DirFS(applyCancelFixture)); err != nil {
		t.Fatalf("copying the fixture: %v", err)
	}

	target := filepath.Join(module, filepath.FromSlash(applyCancelTarget))

	original, err := os.ReadFile(target) //nolint:gosec // a test-owned copy of a fixture.
	if err != nil {
		t.Fatalf("reading the target before the run: %v", err)
	}

	scratch := t.TempDir()
	receiptPath := filepath.Join(scratch, "receipt.json")
	stdoutPath := filepath.Join(scratch, "stdout.json")
	stderrPath := filepath.Join(scratch, "stderr.txt")
	codePath := filepath.Join(scratch, "exit-code.txt")

	//nolint:gosec,noctx // re-executing this test binary is the standard helper-process pattern.
	command := exec.Command(os.Args[0],
		"-test.run=TestRunTheCommandLineForAnInterruptedApplyHelper", "-test.timeout=180s")
	command.Env = append(os.Environ(),
		applyCancelChildEnv+"=1",
		applyCancelModuleEnv+"="+module,
		applyCancelReceiptEnv+"="+receiptPath,
		applyCancelStdoutEnv+"="+stdoutPath,
		applyCancelStderrEnv+"="+stderrPath,
		applyCancelCodeEnv+"="+codePath,
		// The run's own CPU profile teardown is the window the interruption has
		// to land in: it is the cost between the commit and the report, and it
		// is a teardown the user can ask for, not a product hook.
		applyCancelProfileEnv+"="+filepath.Join(scratch, "cpu.pprof"),
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = time.Second

	done := make(chan error, 1)

	if startErr := command.Start(); startErr != nil {
		t.Fatalf("starting helper: %v", startErr)
	}

	reaped := false

	t.Cleanup(func() {
		if reaped {
			return
		}

		_ = command.Process.Signal(syscall.SIGINT)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	})

	go func() {
		done <- command.Wait()
	}()

	code := interruptAfterTheCommit(t, command, target, string(original), done)
	reaped = true

	// The helper's exit code travels in a file: Go's testing package treats an
	// os.Exit(0) inside a test as an early exit and panics, so a clean run ends
	// by returning and the file carries the verdict either way.
	if recorded := strings.TrimSpace(readIfPresent(t, codePath)); recorded != "" {
		if interpreted, convErr := strconv.Atoi(recorded); convErr == nil {
			code = interpreted
		}
	}

	if current := readIfPresent(t, target); current == string(original) {
		t.Fatal("the apply never renamed the test file; the interruption was not staged after a commit")
	}

	// The receipt has two channels: standard output, and the JSON artefact the
	// invocation asked for. A caller reading either is owed the files that
	// landed.
	stdout := readIfPresent(t, stdoutPath)
	receipt := readIfPresent(t, receiptPath)
	published := stdout + receipt

	if code == report.ExitClean {
		// The signal arrived after the report was already published: the write
		// is reported as an ordinary applied suggestion and there is no
		// interruption to mark.
		if !strings.Contains(published, applyCancelTarget) {
			t.Fatalf("the run applied a write and reported nothing about it:\n%s", published)
		}

		return
	}

	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d (operational) or %d (clean, already published)\n%s",
			code, report.ExitOperational, report.ExitClean, readIfPresent(t, stderrPath))
	}

	if !strings.Contains(published, applyCancelTarget) {
		t.Fatalf("the invocation committed %s and exited operationally without reporting it\n"+
			"stdout: %q\nstderr: %q", applyCancelTarget, stdout, readIfPresent(t, stderrPath))
	}

	// A receipt is not a score: the report has to say the analysis was
	// interrupted, so a consumer cannot read the numbers as a completed run.
	document := map[string]any{}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("standard output is not the JSON report it was asked for: %v\n%s", err, stdout)
	}

	interrupted, ok := document["interrupted"].(map[string]any)
	if !ok {
		t.Fatalf("the interrupted receipt does not say the analysis was interrupted:\n%s", stdout)
	}

	if reason, _ := interrupted["reason"].(string); reason == "" {
		t.Fatalf("the interruption carries no reason:\n%s", stdout)
	}

	written, _ := document["apply"].(map[string]any)["written"].([]any)
	if len(written) == 0 {
		t.Fatalf("the receipt names no committed file:\n%s", stdout)
	}

	if reportWritten, _ := written[0].(string); reportWritten != applyCancelTarget {
		t.Fatalf("the receipt names %q, want the committed %s", reportWritten, applyCancelTarget)
	}
}

// interruptAfterTheCommit polls the apply target until the rename lands, then
// interrupts the helper and reaps it. It returns the helper's exit code.
func interruptAfterTheCommit(
	t *testing.T, command *exec.Cmd, target, original string, done <-chan error,
) int {
	t.Helper()

	deadline := time.Now().Add(4 * time.Minute)
	signalled := false

	for {
		select {
		case err := <-done:
			if !signalled {
				t.Fatalf("the run exited before the apply renamed the test file: %v", err)
			}

			exitErr := &exec.ExitError{}
			if err != nil && !errors.As(err, &exitErr) {
				t.Fatalf("helper exit: %v", err)
			}

			if err == nil {
				return report.ExitClean
			}

			return exitErr.ExitCode()
		default:
		}

		if !signalled {
			if content, readErr := os.ReadFile(target); readErr == nil && string(content) != original { //nolint:gosec // a test-owned copy.
				if signalErr := command.Process.Signal(syscall.SIGINT); signalErr != nil {
					t.Fatalf("interrupting helper: %v", signalErr)
				}

				signalled = true
			}
		}

		if time.Now().After(deadline) {
			t.Fatal("the helper never applied an assertion")
		}

		time.Sleep(200 * time.Microsecond)
	}
}

// TestRunTheCommandLineForAnInterruptedApplyHelper is the child half of the
// reproduction. It only does anything when the parent asks for it.
//
//nolint:paralleltest // the helper must own the process and its exit code.
func TestRunTheCommandLineForAnInterruptedApplyHelper(t *testing.T) {
	if os.Getenv(applyCancelChildEnv) != "1" {
		t.Skip("helper process only")
	}

	stdout := mustCreate(t, os.Getenv(applyCancelStdoutEnv))
	stderr := mustCreate(t, os.Getenv(applyCancelStderrEnv))

	// No t.Parallel, no t.Context: the signal handler inside execute must be
	// the thing that ends this run, exactly as it ends the process main runs.
	code := execute(suggestCommand, "dev", []string{
		"--all-verified", "--no-cache", "--jobs", "1",
		reporterFlag, reporterJSON,
		"--" + outputFlagName, reporterJSON + "=" + os.Getenv(applyCancelReceiptEnv),
		"--" + cpuProfileFlag, os.Getenv(applyCancelProfileEnv),
		os.Getenv(applyCancelModuleEnv),
	}, stdout, stderr)

	for _, file := range []*os.File{stdout, stderr} {
		if err := file.Close(); err != nil {
			t.Fatalf("closing the helper's capture file: %v", err)
		}
	}

	if err := os.WriteFile(os.Getenv(applyCancelCodeEnv),
		[]byte(strconv.Itoa(code)), 0o600); err != nil {
		t.Fatalf("recording the exit code: %v", err)
	}

	if code != 0 {
		// The command line's own exit code, published the way main publishes it.
		//revive:disable-next-line:deep-exit
		os.Exit(code)
	}
}

// mustCreate opens the helper's capture file, which the parent reads either
// way: an empty file is the absence of a receipt, not a reason to panic.
func mustCreate(t *testing.T, path string) *os.File {
	t.Helper()

	file, err := os.Create(path) //nolint:gosec // a test-owned scratch path.
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	return file
}

// readIfPresent reads a file the helper may never have written.
func readIfPresent(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // a test-owned scratch path.
	if err != nil {
		return ""
	}

	return string(content)
}
