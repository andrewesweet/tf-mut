package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
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
// the report is published. The same defect has a second window, past the
// engine's last decision, and the deterministic case stages that one instead:
// the run completes, and the cancellation is the one the command line always
// observes for itself.

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

// applyCancelInterruption is the interruption block a receipt carries, and
// applyCancelApply the block naming the files an apply committed.
type applyCancelInterruption struct {
	Reason          string   `json:"reason"`
	CommittedWrites []string `json:"committed_writes"`
}

type applyCancelApply struct {
	Written []string `json:"written"`
}

// applyCancelReceipt is the part of the published document a write command owes
// its caller: the interruption, and the files it committed.
type applyCancelReceipt struct {
	Interrupted *applyCancelInterruption `json:"interrupted"`
	Apply       *applyCancelApply        `json:"apply"`
}

// applyCancelOutcome is what the interrupted helper published.
type applyCancelOutcome struct {
	code     int
	stdout   string
	artefact string
	stderr   string
}

// applyCancelStaging is the copied fixture the invocation applies to: the
// module, the file the apply rewrites, and the bytes it started from.
type applyCancelStaging struct {
	module   string
	target   string
	original string
}

// TestASigintAfterAnApplyRenameStillReportsTheCommittedWrite holds the write
// receipt across cancellation: an invocation that committed a file before the
// signal reports that file, and reports it as a receipt of committed effects
// rather than as a completed score.
func TestASigintAfterAnApplyRenameStillReportsTheCommittedWrite(t *testing.T) {
	t.Parallel()

	if os.Getenv(applyCancelChildEnv) == "1" {
		t.Skip("child process runs the helper instead")
	}

	staged := applyCancelStage(t)
	published := interruptAfterTheCommit(t, staged)

	if landed := readIfPresent(t, staged.target); landed == staged.original {
		t.Fatal("the apply never renamed the test file; the interruption was not staged after a commit")
	}

	switch published.code {
	case report.ExitClean:
		// The signal arrived after the report was already published: the write
		// is reported as an ordinary applied suggestion, which is what a
		// completed invocation promises.
		assertAppliedWrite(t, "standard output", published.stdout)
		assertAppliedWrite(t, "the requested artefact", published.artefact)
	case report.ExitOperational:
		assertInterruptedReceipt(t, "standard output", published.stdout)
		assertInterruptedReceipt(t, "the requested artefact", published.artefact)
	default:
		t.Fatalf("exit code = %d, want %d (already published) or %d (interrupted)\n%s",
			published.code, report.ExitClean, report.ExitOperational, published.stderr)
	}
}

// TestACancelledInvocationStillPublishesTheWritesItCommitted is the
// deterministic half: the run itself completes against the real fixture, and
// the cancellation is the one the command line can always observe for itself —
// a signal that arrives after the engine has returned its report, which used to
// discard a report whose writes had already landed.
func TestACancelledInvocationStillPublishesTheWritesItCommitted(t *testing.T) {
	t.Parallel()

	staged := applyCancelStage(t)
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")

	parsed, err := parse(suggestCommand, "dev", []string{
		"--all-verified", "--no-cache", "--" + jobsFlag, "1",
		reporterFlag, reporterJSON,
		"--" + outputFlagName, reporterJSON + "=" + receiptPath,
		staged.module,
	}, io.Discard)
	if err != nil {
		t.Fatalf("parsing the command line: %v", err)
	}

	result, err := engine.Run(t.Context(), parsed.config)
	if err != nil {
		t.Fatalf("running the invocation: %v", err)
	}

	if result.Apply == nil || len(result.Apply.Written) == 0 {
		t.Fatalf("the invocation committed nothing, so there is no receipt to hold: %+v", result.Apply)
	}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	if code := publish(&stdout, &stderr, parsed, result, context.Canceled); code != report.ExitOperational {
		t.Fatalf("a cancelled invocation with committed writes exits %d, want %d\n%s",
			code, report.ExitOperational, stderr.String())
	}

	if !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("the cancellation is not reported:\n%s", stderr.String())
	}

	assertInterruptedReceipt(t, "standard output", stdout.String())
	assertInterruptedReceipt(t, "the requested artefact", readIfPresent(t, receiptPath))

	// The receipt is not a score: the report's own gate table cannot pass it.
	receipt, committed := result.Interrupt(context.Canceled)
	if !committed {
		t.Fatal("the marked report lost its receipt")
	}

	if code := receipt.ExitCode(parsed.gate); code != report.ExitOperational {
		t.Fatalf("the interrupted receipt's gate exit is %d, want %d", code, report.ExitOperational)
	}
}

// applyCancelStage copies the offline fixture into a scratch directory, so the
// invocation's writes land on the test's own tree.
func applyCancelStage(t *testing.T) applyCancelStaging {
	t.Helper()

	module := filepath.Join(t.TempDir(), "suggest-basic")
	if err := os.CopyFS(module, os.DirFS(applyCancelFixture)); err != nil {
		t.Fatalf("copying the fixture: %v", err)
	}

	target := filepath.Join(module, filepath.FromSlash(applyCancelTarget))

	original, err := os.ReadFile(target) //nolint:gosec // a test-owned copy of a fixture.
	if err != nil {
		t.Fatalf("reading the target before the run: %v", err)
	}

	return applyCancelStaging{module: module, target: target, original: string(original)}
}

// interruptAfterTheCommit runs the command line in a helper process, interrupts
// it the instant the apply's rename changes the target, and hands back its exit
// code and the two channels a caller reads a receipt from.
func interruptAfterTheCommit(t *testing.T, staged applyCancelStaging) applyCancelOutcome {
	t.Helper()

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
		applyCancelModuleEnv+"="+staged.module,
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
	if err := command.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
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

	code := pollForTheCommit(t, command, staged, done)
	reaped = true

	// The helper's exit code travels in a file: Go's testing package treats an
	// os.Exit(0) inside a test as an early exit and panics, so a clean run ends
	// by returning and the file carries the verdict either way.
	if recorded := strings.TrimSpace(readIfPresent(t, codePath)); recorded != "" {
		if interpreted, convErr := strconv.Atoi(recorded); convErr == nil {
			code = interpreted
		}
	}

	return applyCancelOutcome{
		code:     code,
		stdout:   readIfPresent(t, stdoutPath),
		artefact: readIfPresent(t, receiptPath),
		stderr:   readIfPresent(t, stderrPath),
	}
}

// pollForTheCommit watches the apply's target and interrupts the helper the
// moment its bytes change, which is the moment after the atomic rename.
func pollForTheCommit(
	t *testing.T, command *exec.Cmd, staged applyCancelStaging, done <-chan error,
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
			content, readErr := os.ReadFile(staged.target)
			if readErr == nil && string(content) != staged.original {
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

// assertInterruptedReceipt holds what an interrupted invocation owes the caller
// on one channel: the interruption naming its reason, and the committed file.
func assertInterruptedReceipt(t *testing.T, name, content string) {
	t.Helper()

	receipt := decodeApplyCancelReceipt(t, name, content)
	if receipt.Interrupted == nil {
		t.Fatalf("%s does not say the invocation was interrupted:\n%s", name, content)
	}

	if receipt.Interrupted.Reason == "" {
		t.Fatalf("%s carries an interruption with no reason:\n%s", name, content)
	}

	for _, writes := range [][]string{receipt.Interrupted.CommittedWrites, receipt.ApplyWritten()} {
		if !slices.Equal(writes, []string{applyCancelTarget}) {
			t.Fatalf("%s names %v as written, want exactly %s:\n%s",
				name, writes, applyCancelTarget, content)
		}
	}
}

// assertAppliedWrite holds what a completed invocation owes the caller on one
// channel: the file it applied, and no interruption to qualify it.
func assertAppliedWrite(t *testing.T, name, content string) {
	t.Helper()

	receipt := decodeApplyCancelReceipt(t, name, content)
	if receipt.Interrupted != nil {
		t.Fatalf("%s marks a completed invocation interrupted:\n%s", name, content)
	}

	if !slices.Equal(receipt.ApplyWritten(), []string{applyCancelTarget}) {
		t.Fatalf("%s names %v as written, want exactly %s:\n%s",
			name, receipt.ApplyWritten(), applyCancelTarget, content)
	}
}

// ApplyWritten lists the files the receipt records, or nothing when the
// document carries no apply block at all.
func (r applyCancelReceipt) ApplyWritten() []string {
	if r.Apply == nil {
		return nil
	}

	return r.Apply.Written
}

func decodeApplyCancelReceipt(t *testing.T, name, content string) applyCancelReceipt {
	t.Helper()

	receipt := applyCancelReceipt{}
	if err := json.Unmarshal([]byte(content), &receipt); err != nil {
		t.Fatalf("%s is not the JSON report it was asked for: %v\n%s", name, err, content)
	}

	return receipt
}

// TestRunTheCommandLineForAnInterruptedApplyHelper is the child half of the
// reproduction. It only does anything when the parent asks for it.
//
//nolint:paralleltest // the helper must own the run and its exit code.
func TestRunTheCommandLineForAnInterruptedApplyHelper(t *testing.T) {
	if os.Getenv(applyCancelChildEnv) != "1" {
		t.Skip("helper process only")
	}

	stdout := mustCreate(t, os.Getenv(applyCancelStdoutEnv))
	stderr := mustCreate(t, os.Getenv(applyCancelStderrEnv))

	// No t.Parallel, no t.Context: the signal handler inside execute must be
	// the thing that ends this run, exactly as it ends the process main runs.
	code := execute(suggestCommand, "dev", []string{
		"--all-verified", "--no-cache", "--" + jobsFlag, "1",
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

	codePath := os.Getenv(applyCancelCodeEnv)
	recorded := []byte(strconv.Itoa(code))

	if err := os.WriteFile(codePath, recorded, 0o600); err != nil { //nolint:gosec // the parent's scratch path.
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
