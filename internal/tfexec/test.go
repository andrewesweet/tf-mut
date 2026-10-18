package tfexec

import (
	"context"
	"io"
	"time"
)

// Run statuses reported by terraform test -json.
const (
	StatusPass  = "pass"
	StatusFail  = "fail"
	StatusError = "error"
	StatusSkip  = "skip"
)

// RunOutcome is the result of one executed run block.
type RunOutcome struct {
	File   string `json:"file"`
	Run    string `json:"run"`
	Status string `json:"status"`
	// ElapsedMS is the run block's wall time in milliseconds, measured from
	// the stream's own timestamps: the starting and completion events the
	// invocation already emits. Nil when the stream carried no pair to
	// measure — a run block skipped after an earlier error completes without
	// a starting event, and a stream cut short never completes the run at
	// all. Timing decides nothing; absence is never a verdict.
	ElapsedMS *int64 `json:"elapsed_ms,omitempty"`
}

// FileTiming is one test file's wall time, measured from the stream's own
// timestamps: the test file's starting and completion events. The file's
// duration spans its run blocks and the teardown between them, so it is not
// the sum of its runs' elapsed times.
type FileTiming struct {
	// File is the test file's path, as the stream names it.
	File string `json:"file"`
	// Runs is the number of run blocks the file reported, executed or skipped.
	Runs int `json:"runs"`
	// ElapsedMS is the file's wall time in milliseconds; nil when the stream
	// carried no starting and completion pair for the file.
	ElapsedMS *int64 `json:"elapsed_ms,omitempty"`
}

// Executed reports whether the run block actually executed.
//
// Terraform reports run blocks skipped after an earlier error with the same
// completion message as executed ones, so the status is the discriminator.
func (o RunOutcome) Executed() bool {
	return o.Status == StatusPass || o.Status == StatusFail || o.Status == StatusError
}

// TestResult is the decoded outcome of one terraform test invocation.
//
// The per-run outcomes are the source of truth, not the test_summary message:
// the aggregate state is assigned from them by an explicit precedence, and a
// summary that disagreed with them would be a bug rather than a tiebreak.
type TestResult struct {
	Runs        []RunOutcome
	Diagnostics []Diagnostic
	// Payloads holds the per-run plan or state documents a verbose run emits,
	// and is empty for a plain one.
	Payloads []RunPayload
	// Files holds one timing entry per test file that completed, in
	// completion order, and is empty when no file completed.
	Files []FileTiming
	// Started remains true when execution began but output decoding failed.
	// Setup or process-start failures leave it false.
	Started  bool
	ExitCode int
	TimedOut bool
	Duration time.Duration
	// Usage is the invocation's child-process accounting.
	Usage Usage
}

// ExecutedRuns counts the run blocks that actually executed.
func (t TestResult) ExecutedRuns() int {
	executed := 0

	for _, run := range t.Runs {
		if run.Executed() {
			executed++
		}
	}

	return executed
}

// HasStatus reports whether any executed run finished with the given status.
func (t TestResult) HasStatus(status string) bool {
	for _, run := range t.Runs {
		if run.Status == status {
			return true
		}
	}

	return false
}

// FailedRuns lists the run blocks that failed an assertion or errored.
func (t TestResult) FailedRuns() []RunOutcome {
	failed := []RunOutcome{}

	for _, run := range t.Runs {
		if run.Status == StatusFail || run.Status == StatusError {
			failed = append(failed, run)
		}
	}

	return failed
}

// TestOptions selects which test files run, how verbosely, and for how long.
type TestOptions struct {
	// TestDirectory is passed to -test-directory when set.
	TestDirectory string
	// Filters restricts execution to the named test files.
	Filters []string
	// Verbose requests the per-run plan and state payloads.
	Verbose bool
	// Timeout bounds the invocation; zero means no bound.
	Timeout time.Duration
}

// Test runs the test suite of the module in dir.
//
// The stream is decoded as it arrives rather than buffered: a verbose run emits
// the whole provider schema per run block, which is 19.5 MB against
// `hashicorp/aws` and grows with the suite.
func (r Runner) Test(ctx context.Context, dir string, options TestOptions) (TestResult, error) {
	args := []string{"test", "-json", "-no-color"}
	if options.Verbose {
		args = append(args, "-verbose")
	}

	if options.TestDirectory != "" {
		args = append(args, "-test-directory="+options.TestDirectory)
	}

	for _, filter := range options.Filters {
		args = append(args, "-filter="+filter)
	}

	runCtx := ctx

	if options.Timeout > 0 {
		var cancel context.CancelFunc

		runCtx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	decoded := TestResult{} // replaced by the decoder.

	result, err := r.StreamOutput(runCtx, dir, func(stdout io.Reader) error {
		var decodeErr error

		decoded, decodeErr = decodeTestStream(stdout)

		return decodeErr
	}, args...)

	decoded.ExitCode = result.ExitCode
	decoded.Started = result.Started
	decoded.TimedOut = result.TimedOut
	decoded.Duration = result.Duration
	decoded.Usage = result.Usage

	// A timeout kills Terraform mid-message, so the truncation it leaves behind
	// is the timeout, not a malformed stream. Every other decoding failure is an
	// operational failure and must never reach the classifier.
	if err != nil && !result.TimedOut {
		return decoded, err
	}

	return decoded, nil
}

// testRunMessage is the test_run payload of the JSON stream.
type testRunMessage struct {
	Path     string `json:"path"`
	Run      string `json:"run"`
	Progress string `json:"progress"`
	Status   string `json:"status"`
}

// testFileMessage is the test_file payload of the JSON stream.
type testFileMessage struct {
	Path     string `json:"path"`
	Progress string `json:"progress"`
	Status   string `json:"status"`
}

const progressDone = "complete"
