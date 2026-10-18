package tfexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// ErrMalformedStream reports output the decoder could not read to the end.
//
// It is never a verdict: a mutant whose stream was truncated has not been
// evaluated, and reporting it as a survivor would invent a result.
var ErrMalformedStream = errors.New("terraform output could not be decoded")

// streamBufferSize is the read-ahead handed to the JSON decoder. It bounds the
// copy the decoder performs, not the size of a message: the decoder walks a
// message token by token and never holds a whole one.
const streamBufferSize = 64 * 1024

// StreamOutput executes Terraform in dir and hands stdout to consume as it
// arrives, so that output larger than memory can be decoded incrementally.
//
// The remainder of stdout is drained after consume returns, because a consumer
// that stops early would otherwise block Terraform on a full pipe.
func (r Runner) StreamOutput(
	ctx context.Context,
	dir string,
	consume func(io.Reader) error,
	args ...string,
) (Result, error) {
	full := append([]string{"-chdir=" + dir}, args...)
	// The shared constructor stops the whole process group at the deadline, so
	// the provider plugins Terraform started cannot outlive the run.
	command := GroupCommand(ctx, r.Binary, full...)
	command.Env = append(inheritedEnvironment(), r.Env...)

	stderr := bytes.Buffer{}
	command.Stderr = &stderr

	started := time.Now()

	pipe, err := command.StdoutPipe()
	if err != nil {
		return notStarted(ctx, started, err, r.Binary)
	}

	// A deadline that expires before the process starts is still the timeout,
	// not an operational failure: reporting it as one would turn a loaded
	// machine into a run the caller cannot trust.
	if err := command.Start(); err != nil {
		return notStarted(ctx, started, err, r.Binary)
	}

	consumeErr := consume(bufio.NewReaderSize(pipe, streamBufferSize))
	_, _ = io.Copy(io.Discard, pipe)

	result := completed(ctx, command, started, &stderr)

	if result.Err != nil {
		return result.Result, result.Err
	}

	return result.Result, consumeErr
}

// outcome pairs a decoded result with the operational error, if any, that the
// invocation itself produced.
type outcome struct {
	Result Result
	Err    error
}

// completed reaps the child and reads its outcome. It takes the stderr buffer
// itself, not its bytes: Wait must run before the buffer is read, because only
// Wait guarantees the copy out of the stderr pipe has finished — a snapshot
// taken first loses a failing child's diagnostics under load.
func completed(ctx context.Context, command *exec.Cmd, started time.Time, stderr *bytes.Buffer) outcome {
	waitErr := command.Wait()
	// Terraform may leave provider plugins in its process group after a normal
	// exit. Clean up the group after Wait even when cancellation never ran.
	ReapGroup(command)

	result := Result{
		Started:  true,
		Stdout:   nil,
		Stderr:   stderr.Bytes(),
		ExitCode: exitCodeUnavailable,
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
		Duration: time.Since(started),
		Usage:    childUsage(command.ProcessState),
	}

	if err := ctx.Err(); err != nil && !result.TimedOut {
		return outcome{Result: result, Err: fmt.Errorf("%w: %w", ErrNotExecuted, err)}
	}

	if waitErr == nil {
		result.ExitCode = 0

		return outcome{Result: result, Err: nil}
	}

	exitErr := &exec.ExitError{}
	if errors.As(waitErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()

		return outcome{Result: result, Err: nil}
	}

	if result.TimedOut {
		return outcome{Result: result, Err: nil}
	}

	return outcome{Result: result, Err: fmt.Errorf("%w: %w", ErrNotExecuted, waitErr)}
}

// RunPayload is one run block's `test_plan` or `test_state` document with
// `provider_schemas` discarded.
//
// Discarding happens during decoding rather than after it: `-verbose` embeds
// the full provider schema in every per-run message — 19.5 MB per run block
// against `hashicorp/aws` — so a decoder that materialised the message first
// would allocate the whole stream regardless of what it kept.
type RunPayload struct {
	// File is the test file the run block belongs to.
	File string
	// Run is the run block name.
	Run string
	// Kind is plan for a `test_plan` message and state for a `test_state` one.
	Kind string
	// Value is the payload document minus `provider_schemas`.
	Value map[string]any
}

// The two payload kinds a verbose run emits.
const (
	PayloadPlan  = "plan"
	PayloadState = "state"
)

// The message and field names of the test stream.
const (
	messageTestFile   = "test_file"
	messageTestPlan   = "test_plan"
	messageTestState  = "test_state"
	messageTestRun    = "test_run"
	messageDiagnostic = "diagnostic"
	fieldTestFile     = "@testfile"
	fieldTestRun      = "@testrun"
	fieldProviders    = "provider_schemas"
	fieldTimestamp    = "@timestamp"
)

// The progress values a test file's or run block's events carry. A run
// skipped after an earlier error completes without ever starting.
const progressStarting = "starting"

// decodeTestStream reads the whole `terraform test -json` stream.
//
// The stream is walked token by token so that the large payload members can be
// skipped without being held: see RunPayload.
func decodeTestStream(reader io.Reader) (TestResult, error) {
	result := TestResult{
		Runs:        []RunOutcome{},
		Diagnostics: []Diagnostic{},
		Payloads:    []RunPayload{},
		Files:       []FileTiming{},
		Started:     false,
		ExitCode:    exitCodeUnavailable,
		TimedOut:    false,
		Duration:    0,
	}

	// The pairing of start events to completions is decoder-local: a run
	// block's or file's elapsed time needs both of its own events, and
	// neither outlives the stream.
	clock := newStreamClock()

	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	for {
		if err := decodeMessage(decoder, &result, clock); err != nil {
			if errors.Is(err, io.EOF) {
				return result, nil
			}

			return result, fmt.Errorf("%w: %w", ErrMalformedStream, err)
		}
	}
}

// streamClock remembers the start events a run block's or file's elapsed time
// is paired against. Every stream message carries a timestamp, but only the
// starting and completion pair brackets the work, so the clock stores one
// start per run block and one per file, keyed by the path the events name.
type streamClock struct {
	runStarts  map[string]time.Time
	fileStarts map[string]time.Time
}

func newStreamClock() *streamClock {
	return &streamClock{runStarts: map[string]time.Time{}, fileStarts: map[string]time.Time{}}
}

// rememberRun records a run block's starting timestamp, and rememberFile a
// test file's.
func (c *streamClock) rememberRun(file, run string, at *time.Time) {
	if at != nil {
		c.runStarts[file+"\x00"+run] = *at
	}
}

func (c *streamClock) rememberFile(file string, at *time.Time) {
	if at != nil {
		c.fileStarts[file] = *at
	}
}

// runElapsed pairs a completion timestamp with its run block's remembered
// start. Either side missing — the skipped run's case — leaves the elapsed
// time absent rather than zero.
func (c *streamClock) runElapsed(file, run string, completed *time.Time) *int64 {
	start := c.runStarts[file+"\x00"+run]
	return elapsedBetween(start, completed)
}

// fileElapsed pairs a file's completion timestamp with its remembered start.
func (c *streamClock) fileElapsed(file string, completed *time.Time) *int64 {
	start := c.fileStarts[file]
	return elapsedBetween(start, completed)
}

// elapsedBetween measures two wall-clock instants in whole milliseconds. A
// nil or zero side leaves the measurement absent: an absent measurement is
// honest, a zero one is a claim about a fast machine.
func elapsedBetween(start time.Time, completed *time.Time) *int64 {
	if completed == nil || start.IsZero() {
		return nil
	}

	elapsed := completed.Sub(start).Milliseconds()

	return &elapsed
}

// message is the subset of one stream message the decoder retains.
type message struct {
	file      string
	run       string
	timestamp *time.Time
	outcome   *RunOutcome
	payload   *RunPayload
	problem   *Diagnostic
	timing    *FileTiming
}

func decodeMessage(decoder *json.Decoder, result *TestResult, clock *streamClock) error {
	if err := expectDelim(decoder, '{'); err != nil {
		return err
	}

	found := message{}

	for decoder.More() {
		key, err := readKey(decoder)
		if err != nil {
			return err
		}

		if err := decodeMember(decoder, key, &found, clock); err != nil {
			return err
		}
	}

	if err := expectDelim(decoder, '}'); err != nil {
		return err
	}

	attach(found, result)

	return nil
}

func decodeMember(decoder *json.Decoder, key string, found *message, clock *streamClock) error {
	switch key {
	case fieldTestFile:
		return decoder.Decode(&found.file)
	case fieldTestRun:
		return decoder.Decode(&found.run)
	case fieldTimestamp:
		return decodeTimestamp(decoder, found)
	case messageTestFile:
		return decodeFileTiming(decoder, found, clock)
	case messageTestRun:
		return decodeRunOutcome(decoder, found, clock)
	case messageDiagnostic:
		problem := Diagnostic{} // decoded from the stream.
		if err := decoder.Decode(&problem); err != nil {
			return err
		}

		found.problem = &problem

		return nil
	case messageTestPlan:
		return decodePayload(decoder, PayloadPlan, found)
	case messageTestState:
		return decodePayload(decoder, PayloadState, found)
	default:
		return skipValue(decoder)
	}
}

// decodeTimestamp reads a message's own timestamp. One it cannot parse is a
// timing gap, not a malformed stream: the message's other members stand, and
// the pairing degrades to absent.
func decodeTimestamp(decoder *json.Decoder, found *message) error {
	raw := "" // decoded from the stream.
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil //nolint:nilerr // an unparseable timestamp is a timing gap, not a malformed stream.
	}

	found.timestamp = &parsed

	return nil
}

// decodeFileTiming records a test file event: its start, for the pairing, or
// its completion, which is where the file's elapsed time is measured.
func decodeFileTiming(decoder *json.Decoder, found *message, clock *streamClock) error {
	event := testFileMessage{} // decoded from the stream.
	if err := decoder.Decode(&event); err != nil {
		return err
	}

	switch event.Progress {
	case progressStarting:
		clock.rememberFile(event.Path, found.timestamp)
	case progressDone:
		elapsed := clock.fileElapsed(event.Path, found.timestamp)
		found.timing = &FileTiming{File: event.Path, ElapsedMS: elapsed}
	default:
		// A progress value this decoder does not pair — running, teardown —
		// carries no timing it publishes.
	}

	return nil
}

// decodeRunOutcome records a run block event: its start, for the pairing, or
// its completion, which is both the published outcome and the instant the
// run's elapsed time is measured at.
func decodeRunOutcome(decoder *json.Decoder, found *message, clock *streamClock) error {
	run := testRunMessage{} // decoded from the stream.
	if err := decoder.Decode(&run); err != nil {
		return err
	}

	if run.Progress == progressStarting {
		clock.rememberRun(run.Path, run.Run, found.timestamp)

		return nil
	}

	if run.Progress != progressDone {
		return nil
	}

	found.outcome = &RunOutcome{
		File:      run.Path,
		Run:       run.Run,
		Status:    run.Status,
		ElapsedMS: clock.runElapsed(run.Path, run.Run, found.timestamp),
	}

	return nil
}

// decodePayload reads a payload document, skipping `provider_schemas` without
// materialising it.
func decodePayload(decoder *json.Decoder, kind string, found *message) error {
	if err := expectDelim(decoder, '{'); err != nil {
		return err
	}

	value := map[string]any{}

	for decoder.More() {
		key, err := readKey(decoder)
		if err != nil {
			return err
		}

		if key == fieldProviders {
			if err := skipValue(decoder); err != nil {
				return err
			}

			continue
		}

		member := any(nil)
		if err := decoder.Decode(&member); err != nil {
			return err
		}

		value[key] = member
	}

	if err := expectDelim(decoder, '}'); err != nil {
		return err
	}

	found.payload = &RunPayload{File: "", Run: "", Kind: kind, Value: value}

	return nil
}

func attach(found message, result *TestResult) {
	if found.outcome != nil {
		result.Runs = append(result.Runs, *found.outcome)
	}

	if found.problem != nil {
		problem := *found.problem
		problem.TestFile = found.file
		problem.TestRun = found.run
		result.Diagnostics = append(result.Diagnostics, problem)
	}

	if found.payload != nil {
		payload := *found.payload
		payload.File = found.file
		payload.Run = found.run
		result.Payloads = append(result.Payloads, payload)
	}

	if found.timing != nil {
		timing := *found.timing
		// A file completes after every run block it owns, so the count at
		// completion is the count the file reported.
		for _, run := range result.Runs {
			if run.File == timing.File {
				timing.Runs++
			}
		}

		result.Files = append(result.Files, timing)
	}
}

// skipValue consumes the next value without retaining it.
func skipValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}

	depth := 1
	if delim == '}' || delim == ']' {
		return fmt.Errorf("%w: unexpected %s", ErrMalformedStream, delim)
	}

	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}

		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
		}
	}

	return nil
}

func readKey(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}

	key, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%w: object key is %T", ErrMalformedStream, token)
	}

	return key, nil
}

func expectDelim(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok || delim != want {
		return fmt.Errorf("%w: expected %s, found %v", ErrMalformedStream, want, token)
	}

	return nil
}

// notStarted reports a Terraform invocation that never began, distinguishing an
// expired deadline from a genuine failure to execute.
func notStarted(ctx context.Context, started time.Time, cause error, binary string) (Result, error) {
	result := Result{
		Stdout:   nil,
		Stderr:   nil,
		ExitCode: exitCodeUnavailable,
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
		Duration: time.Since(started),
	}

	if result.TimedOut {
		return result, nil
	}

	return result, fmt.Errorf("%w: %s: %w", ErrNotExecuted, binary, cause)
}
