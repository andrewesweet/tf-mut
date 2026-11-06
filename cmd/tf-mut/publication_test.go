package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// #305: publication still lost or misattributed the receipt of writes an
// invocation had already committed. The D6 regression beside this file holds
// the receipt across cancellation with failing in-memory writers; these hold
// the two facts that regression could not: that a descriptor nobody reads must
// not end the process before the outputs that still work carry the receipt, and
// that a receipt line is attributable to its run whether or not the report
// carries a telemetry block.

// The streams the helper can have wired to a pipe with no reader.
const (
	publicationStdoutStream = "stdout"
	publicationStderrStream = "stderr"
	publicationBothStreams  = "both"
)

// publicationRunID is the `[run_id] ` prefix the stderr attribution contract
// requires: the run's one identifier, 128 bits hex-encoded, on every line.
var publicationRunID = regexp.MustCompile(`^\[[0-9a-f]{32}\] `)

// TestABrokenPipeStillCarriesTheReceiptToTheRemainingOutputs is the reported
// broken-descriptor defect: after an apply rename, a stderr consumer that has
// gone (or stdout piped to `head -n 0`) makes the next write raise SIGPIPE, and
// Go's default action for standard output and standard error ends the process
// on the spot — before the outputs that still work carry the receipt of the
// file already on disk.
func TestABrokenPipeStillCarriesTheReceiptToTheRemainingOutputs(t *testing.T) {
	t.Parallel()

	if os.Getenv(applyCancelChildEnv) == "1" {
		t.Skip("child process runs the helper instead")
	}

	for _, broken := range []string{
		publicationStdoutStream, publicationStderrStream, publicationBothStreams,
	} {
		t.Run(broken, func(t *testing.T) {
			t.Parallel()

			staged := applyCancelStage(t)
			published := runInterruptedHelper(t, staged, reporterJSON, broken)

			if landed := readIfPresent(t, staged.target); landed == staged.original {
				t.Fatal("the apply never renamed the test file; the pipe broke before the commit")
			}

			// A descriptor nobody reads must not end the process: whatever the
			// invocation concluded, the exit is its own verdict, not a signal
			// death — the pre-fix failure exits as -1 with no receipt anywhere.
			switch published.code {
			case report.ExitClean, report.ExitOperational:
			default:
				t.Fatalf("the invocation died on the descriptor nobody reads: exit %d\nstderr: %s",
					published.code, published.stderr)
			}

			stdoutBroken := broken == publicationStdoutStream || broken == publicationBothStreams
			stderrBroken := broken == publicationStderrStream || broken == publicationBothStreams

			// The file the caller asked for still records the write. Whether it is
			// marked interrupted depends only on whether the interruption was
			// observed before that file was written, which is the reported
			// interleaving's business, not the broken descriptor's.
			assertTheChannelNamesTheCommittedWrite(t, "the requested artefact", published.artefact)

			if !stdoutBroken {
				assertTheChannelNamesTheCommittedWrite(t, "standard output", published.stdout)
			}

			if stderrBroken {
				return
			}

			// An operational exit always has something to say about why, and every
			// line it says names the run.
			if published.code != report.ExitClean {
				assertEveryLineCarriesTheRunIdentifier(t, published.stderr)
			}
		})
	}
}

// assertTheChannelNamesTheCommittedWrite holds the claim a broken descriptor
// must not break: the output records the file the invocation committed, either
// as the receipt of an interruption or — when that output was written before the
// interruption was observed — as the applied write it was published with.
func assertTheChannelNamesTheCommittedWrite(t *testing.T, name, content string) {
	t.Helper()

	receipt := decodeApplyCancelReceipt(t, name, content)
	if receipt.Interrupted != nil {
		if !slices.Equal(receipt.Interrupted.CommittedWrites, []string{applyCancelTarget}) {
			t.Fatalf("%s names %v as written, want exactly %s:\n%s",
				name, receipt.Interrupted.CommittedWrites, applyCancelTarget, content)
		}

		return
	}

	if !slices.Equal(receipt.ApplyWritten(), []string{applyCancelTarget}) {
		t.Fatalf("%s names %v as written, want exactly %s:\n%s",
			name, receipt.ApplyWritten(), applyCancelTarget, content)
	}
}

// TestAReceiptLineCarriesTheRunIdentifierWithoutTelemetry holds the attribution
// defect on the path where telemetry cannot help: a characterisation publishes
// no telemetry block at all, so an identifier read from the report is empty and
// every receipt line the command line wrote about committed files went
// unattributed. The identifier has to arrive over the channel the command line
// handed the engine.
func TestAReceiptLineCarriesTheRunIdentifierWithoutTelemetry(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	testsupport.WriteFile(t, filepath.Join(module, "main.tf"),
		"resource \"terraform_data\" \"app\" {\n  input = \"kept\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.output\n}\n")

	identifiers := &runIdentifierWriter{writer: io.Discard}
	parsed, parseErr := parse(characteriseCommand, "dev", []string{
		"--" + writeFlagName, "--" + jobsFlag, "1", reporterFlag, reporterJSON, module,
	}, identifiers)
	if parseErr != nil {
		t.Fatalf("parsing the command line: %v", parseErr)
	}

	result, runErr := engine.Run(t.Context(), parsed.config)
	if runErr != nil {
		t.Fatalf("running the characterisation: %v", runErr)
	}

	if result.Telemetry != nil {
		t.Fatal("a characterisation report carries a telemetry block; the case no longer needs its own channel")
	}

	written := result.Characterisation.Write.Written
	if len(written) == 0 {
		t.Fatalf("the characterisation committed nothing, so there is no receipt to attribute: %+v",
			result.Characterisation)
	}

	if identifiers.Identifier() == "" {
		t.Fatal("the run minted no identifier on the channel the command line supplied")
	}

	stdout, stderr := bytes.Buffer{}, bytes.Buffer{}
	code := publish(publication{
		ctx: t.Context(), stdout: &stdout, stderr: &stderr,
		parsed: parsed, result: result, runErr: context.Canceled,
		runID: identifiers.Identifier(),
	})
	if code != report.ExitOperational {
		t.Fatalf("an interrupted characterisation exits %d, want %d", code, report.ExitOperational)
	}

	// The receipt still reaches the output the caller asked for; what the
	// report cannot say here is which run it belongs to.
	assertInterruptedCharacterisationReceipt(t, "standard output", stdout.String(), written)
	assertCharacterisationInterruptionReceipt(t, stderr.String(), written)
	assertEveryLineCarriesTheRunIdentifier(t, stderr.String())
}

// assertCharacterisationInterruptionReceipt holds the text receipt stderr owes
// for a cancelled characterisation: the interruption and the files it wrote.
func assertCharacterisationInterruptionReceipt(t *testing.T, receipt string, written []string) {
	t.Helper()

	for _, text := range append([]string{"interrupted", "context canceled", "not a score"}, written...) {
		if !strings.Contains(receipt, text) {
			t.Fatalf("stderr receipt lacks %q: %s", text, receipt)
		}
	}
}

// assertInterruptedCharacterisationReceipt holds the receipt a cancelled
// characterisation publishes: the interruption, and the files it wrote.
func assertInterruptedCharacterisationReceipt(
	t *testing.T, name, content string, written []string,
) {
	t.Helper()

	receipt := struct {
		Interrupted *applyCancelInterruption `json:"interrupted"`
	}{}
	if err := json.Unmarshal([]byte(content), &receipt); err != nil {
		t.Fatalf("%s is not the JSON report it was asked for: %v\n%s", name, err, content)
	}

	if receipt.Interrupted == nil {
		t.Fatalf("%s does not say the invocation was interrupted:\n%s", name, content)
	}

	if !slices.Equal(receipt.Interrupted.CommittedWrites, written) {
		t.Fatalf("%s names %v as written, want exactly %v:\n%s",
			name, receipt.Interrupted.CommittedWrites, written, content)
	}
}

// TestACancellationDuringPublicationMarksTheReceiptInTheRemainingOutputs holds
// the late-cancellation defect: after the engine returns successfully from a
// committed apply, a cancellation that arrives while one output is blocked used
// to be swallowed. Publication rendered an unmarked report and exited clean, so
// the caller read a completed score for an invocation that never completed.
func TestACancellationDuringPublicationMarksTheReceiptInTheRemainingOutputs(t *testing.T) {
	t.Parallel()

	staged := applyCancelStage(t)
	scratch := t.TempDir()
	receiptPath := filepath.Join(scratch, "receipt.json")

	identifiers := &runIdentifierWriter{writer: io.Discard}
	parsed, parseErr := parse(suggestCommand, "dev", []string{
		"--" + allVerifiedFlag, noCacheFlag, "--" + jobsFlag, "1",
		reporterFlag, reporterJSON,
		"--" + outputFlagName, reporterJSON + "=" + receiptPath,
		staged.module,
	}, identifiers)
	if parseErr != nil {
		t.Fatalf("parsing the command line: %v", parseErr)
	}

	result, runErr := engine.Run(t.Context(), parsed.config)
	if runErr != nil {
		t.Fatalf("running the invocation: %v", runErr)
	}

	if result.Apply == nil || len(result.Apply.Written) == 0 {
		t.Fatalf("the invocation committed nothing, so there is no receipt to hold: %+v", result.Apply)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stdout := newBlockedWriter()
	stderr := bytes.Buffer{}
	codes := make(chan int, 1)

	go func() {
		codes <- publish(publication{
			ctx: ctx, stdout: stdout, stderr: &stderr,
			parsed: parsed, result: result, runID: identifiers.Identifier(),
		})
	}()

	// The cancellation lands while standard output is blocked on its consumer,
	// which is the window no gate or exit code inside the report reaches.
	<-stdout.blocked
	cancel()
	close(stdout.released)

	if code := <-codes; code != report.ExitOperational {
		t.Fatalf("a cancelled publication exits %d, want %d\n%s", code, report.ExitOperational, &stderr)
	}

	assertInterruptedReceipt(t, "the output that remained", readIfPresent(t, receiptPath))
	assertTextInterruptionReceipt(t, stderr.String())
	assertEveryLineCarriesTheRunIdentifier(t, stderr.String())
}

// blockedWriter blocks on its first write until the test releases it, so a
// cancellation can be landed while publication is mid-output.
type blockedWriter struct {
	blocked  chan struct{}
	released chan struct{}
	advanced sync.Once
	written  bytes.Buffer
}

func newBlockedWriter() *blockedWriter {
	return &blockedWriter{blocked: make(chan struct{}), released: make(chan struct{})}
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.advanced.Do(func() { close(w.blocked) })
	<-w.released

	return w.written.Write(p)
}

// assertEveryLineCarriesTheRunIdentifier holds the stderr attribution contract:
// every line names the run, and all of them name the same run.
func assertEveryLineCarriesTheRunIdentifier(t *testing.T, output string) {
	t.Helper()

	if strings.TrimSpace(output) == "" {
		t.Fatal("no stderr line was attributed: the invocation wrote nothing")
	}

	prefix := ""
	for line := range strings.SplitSeq(strings.TrimSuffix(output, "\n"), "\n") {
		match := publicationRunID.FindString(line)
		if match == "" {
			t.Fatalf("stderr line does not name its run: %q\n%s", line, output)
		}

		if prefix == "" {
			prefix = match
		} else if match != prefix {
			t.Fatalf("stderr lines name two runs, %q and %q:\n%s", prefix, match, output)
		}
	}
}
