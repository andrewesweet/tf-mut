package tfexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	ExitCode    int
	TimedOut    bool
	Duration    time.Duration
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

// TestOptions selects which test files run and for how long.
type TestOptions struct {
	// TestDirectory is passed to -test-directory when set.
	TestDirectory string
	// Filters restricts execution to the named test files.
	Filters []string
	// Timeout bounds the invocation; zero means no bound.
	Timeout time.Duration
}

// Test runs the test suite of the module in dir.
func (r Runner) Test(ctx context.Context, dir string, options TestOptions) (TestResult, error) {
	args := []string{"test", "-json", "-no-color"}
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

	result, err := r.Run(runCtx, dir, args...)
	if err != nil {
		return TestResult{}, err
	}

	decoded := decodeTestStream(result.Stdout)
	decoded.ExitCode = result.ExitCode
	decoded.TimedOut = result.TimedOut
	decoded.Duration = result.Duration

	return decoded, nil
}

// testRunMessage is the test_run payload of the JSON stream.
type testRunMessage struct {
	Path     string `json:"path"`
	Run      string `json:"run"`
	Progress string `json:"progress"`
	Status   string `json:"status"`
}

type testMessage struct {
	Type        string          `json:"type"`
	TestRun     *testRunMessage `json:"test_run"`
	Diagnostic  *Diagnostic     `json:"diagnostic"`
	TestFile    string          `json:"@testfile"`
	TestRunName string          `json:"@testrun"`
}

const (
	scanBufferHint  = 64 * 1024
	scanBufferLimit = 64 * 1024 * 1024
	progressDone    = "complete"
)

func decodeTestStream(stream []byte) TestResult {
	result := TestResult{
		Runs:        []RunOutcome{},
		Diagnostics: []Diagnostic{},
	}

	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 0, scanBufferHint), scanBufferLimit)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		message := testMessage{}
		if err := json.Unmarshal(line, &message); err != nil {
			continue
		}

		switch {
		case message.TestRun != nil && message.TestRun.Progress == progressDone:
			result.Runs = append(result.Runs, RunOutcome{
				File:   message.TestRun.Path,
				Run:    message.TestRun.Run,
				Status: message.TestRun.Status,
			})
		case message.Diagnostic != nil:
			diagnostic := *message.Diagnostic
			diagnostic.TestFile = message.TestFile
			diagnostic.TestRun = message.TestRunName
			result.Diagnostics = append(result.Diagnostics, diagnostic)
		default:
		}
	}

	return result
}
