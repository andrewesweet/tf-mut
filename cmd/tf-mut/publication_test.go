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

			assertTheArtefactReceivedTheReceipt(t, published)
			assertTheWorkingChannelsCarriedTheReceipt(t, broken, published)
		})
	}
}

// assertTheArtefactReceivedTheReceipt holds the claim a broken descriptor must
// not break: the file the caller asked for records the write, marked
// interrupted when the invocation was interrupted.
func assertTheArtefactReceivedTheReceipt(t *testing.T, published applyCancelOutcome) {
	t.Helper()

	switch published.code {
	case report.ExitOperational:
		assertInterruptedReceipt(t, "the requested artefact", published.artefact)
	case report.ExitClean:
		// The signal arrived after the report was published: the write is
		// reported as an ordinary applied suggestion, which is what a
		// completed invocation promises.
		assertAppliedWrite(t, "the requested artefact", published.artefact)
	default:
		t.Fatalf("exit code = %d, want %d (interrupted) or %d (published)\nstderr: %s",
			published.code, report.ExitOperational, report.ExitClean, published.stderr)
	}
}

// assertTheWorkingChannelsCarriedTheReceipt holds the same claim for the
// channels the broken descriptor did not touch: they still carry the receipt,
// and every line on stderr names the run.
func assertTheWorkingChannelsCarriedTheReceipt(
	t *testing.T, broken string, published applyCancelOutcome,
) {
	t.Helper()

	stdoutBroken := broken == publicationStdoutStream || broken == publicationBothStreams
	stderrBroken := broken == publicationStderrStream || broken == publicationBothStreams

	if !stdoutBroken {
		if published.code == report.ExitOperational {
			assertInterruptedReceipt(t, "standard output", published.stdout)
		} else {
			assertAppliedWrite(t, "standard output", published.stdout)
		}
	}

	if stderrBroken {
		return
	}

	if published.code == report.ExitOperational {
		assertTextInterruptionReceipt(t, published.stderr)
	}

	assertEveryLineCarriesTheRunIdentifier(t, published.stderr)
}

// TestAReceiptLineCarriesTheRunIdentifierWithoutTelemetry holds the attribution
// defect on the path where telemetry cannot help: a characterisation publishes
// no telemetry block at all, so an identifier read from the report is empty and
// every receipt line the command line wrote about committed files went
// unattributed.
func TestAReceiptLineCarriesTheRunIdentifierWithoutTelemetry(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	testsupport.WriteFile(t, filepath.Join(module, "main.tf"),
		"resource \"terraform_data\" \"app\" {\n  input = \"kept\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.output\n}\n")

	parsed, parseErr := parse(characteriseCommand, "dev", []string{
		"--" + writeFlagName, "--" + jobsFlag, "1", reporterFlag, reporterJSON, module,
	}, io.Discard)
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

	stdout, stderr := bytes.Buffer{}, bytes.Buffer{}
	if code := publish(&stdout, &stderr, parsed, result, context.Canceled); code != report.ExitOperational {
		t.Fatalf("an interrupted characterisation exits %d, want %d", code, report.ExitOperational)
	}

	// The receipt still reaches the output the caller asked for; what it lacks
	// is the run it belongs to.
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
