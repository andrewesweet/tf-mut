package engine

// D18: phase one ignored Terraform's exit status, so a stream that stopped
// early with a non-zero exit and only passing runs was judged Survived and
// counted in the score. The regression drives the phase-one truncation check
// with recorded short streams: the real binary cannot be driven into producing
// a truncated stream on demand, so the shapes below are taken from the
// reported run (3 of 154 runs, all passing, non-zero exit).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

func recordedStream(exit int, passing int, statuses ...string) tfexec.TestResult {
	runs := make([]tfexec.RunOutcome, 0, passing+len(statuses))
	for i := range passing {
		runs = append(runs, tfexec.RunOutcome{
			File:   "tests/unit.tftest.hcl",
			Run:    fmt.Sprintf("passing%d", i),
			Status: tfexec.StatusPass,
		})
	}

	for i, status := range statuses {
		runs = append(runs, tfexec.RunOutcome{
			File:   "tests/unit.tftest.hcl",
			Run:    fmt.Sprintf("other%d", i),
			Status: status,
		})
	}

	return tfexec.TestResult{Runs: runs, ExitCode: exit}
}

func TestPhaseOneTruncationIsAnExecutionError(t *testing.T) {
	t.Parallel()

	t.Run("non-zero exit with only passing runs", func(t *testing.T) {
		t.Parallel()

		// The D18 shape: 3 of 154 runs recorded, all passing, non-zero exit.
		message, refused := phaseOneTruncation(recordedStream(1, 3), 154, false)
		if !refused {
			t.Fatal("a short stream with a non-zero exit and no failing run was not refused")
		}

		if !strings.Contains(message, "1") {
			t.Fatalf("refusal carries no exit code: %q", message)
		}
	})

	t.Run("short count with exit zero", func(t *testing.T) {
		t.Parallel()

		message, refused := phaseOneTruncation(recordedStream(0, 3), 154, false)
		if !refused {
			t.Fatal("a stream with fewer executed runs than the baseline was not refused")
		}

		if !strings.Contains(message, "3") || !strings.Contains(message, "154") {
			t.Fatalf("refusal names neither count: %q", message)
		}
	})

	t.Run("full passing stream still passes", func(t *testing.T) {
		t.Parallel()

		if message, refused := phaseOneTruncation(recordedStream(0, 154), 154, false); refused {
			t.Fatalf("a full passing stream was refused: %q", message)
		}
	})

	t.Run("failing run still kills", func(t *testing.T) {
		t.Parallel()

		stream := recordedStream(1, 153, tfexec.StatusFail)
		if message, refused := phaseOneTruncation(stream, 154, false); refused {
			t.Fatalf("a failing run was refused instead of killed: %q", message)
		}
	})

	t.Run("erroring run still reaches validate", func(t *testing.T) {
		t.Parallel()

		stream := recordedStream(1, 153, tfexec.StatusError)
		if message, refused := phaseOneTruncation(stream, 154, false); refused {
			t.Fatalf("an erroring run was refused instead of validated: %q", message)
		}
	})

	t.Run("timeout keeps its own handling", func(t *testing.T) {
		t.Parallel()

		stream := recordedStream(1, 3)
		stream.TimedOut = true

		if message, refused := phaseOneTruncation(stream, 154, false); refused {
			t.Fatalf("a timed-out run was refused instead of recorded as a deadline: %q", message)
		}
	})

	t.Run("filtered short count is not a truncation", func(t *testing.T) {
		t.Parallel()

		// A -filter selection legitimately executes fewer runs than the
		// unfiltered baseline, so the count rule applies only unfiltered.
		if message, refused := phaseOneTruncation(recordedStream(0, 3), 154, true); refused {
			t.Fatalf("a filtered short stream was refused: %q", message)
		}
	})

	t.Run("filtered non-zero exit still refuses", func(t *testing.T) {
		t.Parallel()

		message, refused := phaseOneTruncation(recordedStream(1, 3), 154, true)
		if !refused {
			t.Fatal("a filtered short stream with a non-zero exit was not refused")
		}

		if !strings.Contains(message, "1") {
			t.Fatalf("refusal carries no exit code: %q", message)
		}
	})

	t.Run("zero runs stay with the existing refusal", func(t *testing.T) {
		t.Parallel()

		if message, refused := phaseOneTruncation(recordedStream(0, 0), 154, false); refused {
			t.Fatalf("zero runs were refused here instead of by the zero-run refusal: %q", message)
		}
	})
}
