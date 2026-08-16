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
	command := exec.CommandContext(ctx, r.Binary, full...) //nolint:gosec // the binary and arguments are tool-controlled.
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

	result := completed(ctx, command.Wait(), started, stderr.Bytes())

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

func completed(ctx context.Context, waitErr error, started time.Time, stderr []byte) outcome {
	result := Result{
		Stdout:   nil,
		Stderr:   stderr,
		ExitCode: exitCodeUnavailable,
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
		Duration: time.Since(started),
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
	messageTestPlan   = "test_plan"
	messageTestState  = "test_state"
	messageTestRun    = "test_run"
	messageDiagnostic = "diagnostic"
	fieldTestFile     = "@testfile"
	fieldTestRun      = "@testrun"
	fieldProviders    = "provider_schemas"
)

// decodeTestStream reads the whole `terraform test -json` stream.
//
// The stream is walked token by token so that the large payload members can be
// skipped without being held: see RunPayload.
func decodeTestStream(reader io.Reader) (TestResult, error) {
	result := TestResult{
		Runs:        []RunOutcome{},
		Diagnostics: []Diagnostic{},
		Payloads:    []RunPayload{},
		ExitCode:    exitCodeUnavailable,
		TimedOut:    false,
		Duration:    0,
	}

	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	for {
		if err := decodeMessage(decoder, &result); err != nil {
			if errors.Is(err, io.EOF) {
				return result, nil
			}

			return result, fmt.Errorf("%w: %w", ErrMalformedStream, err)
		}
	}
}

// message is the subset of one stream message the decoder retains.
type message struct {
	file    string
	run     string
	outcome *RunOutcome
	payload *RunPayload
	problem *Diagnostic
}

func decodeMessage(decoder *json.Decoder, result *TestResult) error {
	if err := expectDelim(decoder, '{'); err != nil {
		return err
	}

	found := message{file: "", run: "", outcome: nil, payload: nil, problem: nil}

	for decoder.More() {
		key, err := readKey(decoder)
		if err != nil {
			return err
		}

		if err := decodeMember(decoder, key, &found); err != nil {
			return err
		}
	}

	if err := expectDelim(decoder, '}'); err != nil {
		return err
	}

	attach(found, result)

	return nil
}

func decodeMember(decoder *json.Decoder, key string, found *message) error {
	switch key {
	case fieldTestFile:
		return decoder.Decode(&found.file)
	case fieldTestRun:
		return decoder.Decode(&found.run)
	case messageTestRun:
		return decodeRunOutcome(decoder, found)
	case messageDiagnostic:
		problem := Diagnostic{} //nolint:exhaustruct // decoded from the stream.
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

func decodeRunOutcome(decoder *json.Decoder, found *message) error {
	run := testRunMessage{} //nolint:exhaustruct // decoded from the stream.
	if err := decoder.Decode(&run); err != nil {
		return err
	}

	if run.Progress != progressDone {
		return nil
	}

	found.outcome = &RunOutcome{File: run.Path, Run: run.Run, Status: run.Status}

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
