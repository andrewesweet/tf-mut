//nolint:testpackage // the fail-fast stop is a decoder-level contract.
package tfexec

import (
	"bytes"
	"strings"
	"testing"
)

// failFastStream builds a stream whose first run completes fail, followed by
// the runs a complete invocation would still have executed. The lines are the
// recorded stream's own, reordered: a failing assertion is the same message
// shape with the status fail, and the trailing runs are the ones a stopped
// child would never have written but the decoder must still honour if a
// dying child managed to flush them.
func failFastStream(tb testing.TB, firstStatus string) []byte {
	tb.Helper()

	lines := strings.Split(string(recorded(tb)), "\n")
	// Lines 0-4 carry the version, the abstract, the file's start, the run's
	// start and the run's completion; line 11 is the second run's completion.
	completed := strings.Replace(lines[4], `"status":"pass"`, `"status":"`+firstStatus+`"`, 1)

	built := []string{lines[0], lines[1], lines[2], lines[3], completed, lines[9], lines[10], lines[11], ""}

	return []byte(strings.Join(built, "\n"))
}

// failFastDiagStream appends two assertion diagnostics and then a teardown to
// the arming run's completion, the order a failing apply run writes: the
// diagnostics are the evidence the stop must not cut short, and the teardown
// is the first message that proves them flushed. The diagnostic lines are
// real output's own, captured from the killset fixture's failing run.
func failFastDiagStream(tb testing.TB) []byte {
	tb.Helper()

	// The diagnostic line is real output's own, captured from the killset
	// fixture's failing run, wrapped only to keep the line readable.
	const firstDiagnostic = `{"@level":"error","@message":"Error: Test assertion failed",` +
		`"@module":"terraform.ui","@testfile":"tests/unit.tftest.hcl","@testrun":"applied",` +
		`"@timestamp":"2026-10-24T16:54:05.070736+01:00","diagnostic":{"severity":"error",` +
		`"summary":"Test assertion failed","detail":"first assertion",` +
		`"range":{"filename":"tests/unit.tftest.hcl","start":{"line":5,"column":21,"byte":66},` +
		`"end":{"line":5,"column":41,"byte":86}},"snippet":{"context":"run \"applied\"",` +
		`"code":"    condition     = output.app == \"kept\"","start_line":5,` +
		`"highlight_start_offset":20,"highlight_end_offset":40}},"type":"diagnostic"}`

	secondDiagnostic := strings.Replace(firstDiagnostic, "first assertion", "second assertion", 1)

	lines := strings.Split(string(recorded(tb)), "\n")
	completed := strings.Replace(lines[4], `"status":"pass"`, `"status":"fail"`, 1)
	teardown := strings.Replace(lines[4], `"progress":"`+progressDone+`"`, `"progress":"teardown"`, 1)
	teardown = strings.Replace(teardown, `"status":"fail"`, ``, 1)

	built := []string{lines[0], lines[1], lines[2], lines[3], completed, firstDiagnostic, secondDiagnostic, teardown, ""}

	return []byte(strings.Join(built, "\n"))
}

func TestTheStopFiresAfterAFailingRunsDiagnosticsNotAtItsCompletion(t *testing.T) {
	t.Parallel()

	fires := 0
	result, err := decodeTestStreamStopping(bytes.NewReader(failFastDiagStream(t)), func(run RunOutcome) bool {
		return run.Status == StatusFail
	}, func() {
		fires++
	})
	if err != nil {
		t.Fatalf("decoding a stream whose first run failed: %v", err)
	}

	if !result.StoppedOnFail {
		t.Fatal("a stream whose first run failed must record the stop")
	}

	if fires != 1 {
		t.Fatalf("the stop fired %d times, want once", fires)
	}

	if len(result.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %d decoded, want both assertion diagnostics", len(result.Diagnostics))
	}

	// The record ends at the proof message: the arming run is the only run
	// the stopped invocation is credited with.
	if len(result.Runs) != 1 || result.Runs[0].Status != StatusFail {
		t.Fatalf("runs = %v, want only the arming run", result.Runs)
	}
}

func TestAnErrorRunNeverArmsTheStop(t *testing.T) {
	t.Parallel()

	fires := 0
	result, err := decodeTestStreamStopping(bytes.NewReader(failFastStream(t, StatusError)), func(run RunOutcome) bool {
		return run.Status == StatusFail
	}, func() {
		fires++
	})
	if err != nil {
		t.Fatalf("decoding a stream whose first run errored: %v", err)
	}

	if result.StoppedOnFail {
		t.Fatal("an error run must not record a fail stop")
	}

	if fires != 0 {
		t.Fatalf("the stop fired %d times, want never", fires)
	}

	if len(result.Runs) != 2 {
		t.Fatalf("runs = %v, want both run blocks: the error and the run after it", result.Runs)
	}
}

func TestAFullStreamWithoutAStopDecodesEveryRun(t *testing.T) {
	t.Parallel()

	result, err := decodeTestStreamStopping(bytes.NewReader(failFastStream(t, StatusFail)), nil, nil)
	if err != nil {
		t.Fatalf("decoding the whole stream: %v", err)
	}

	if result.StoppedOnFail {
		t.Fatal("a stream read to its end must not record a stop")
	}

	if len(result.Runs) != 2 {
		t.Fatalf("runs = %v, want both run blocks", result.Runs)
	}
}
