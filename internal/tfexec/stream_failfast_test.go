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

	built := []string{
		lines[0], lines[1], lines[2], lines[3], completed,
		firstDiagnostic, secondDiagnostic, teardown,
		strings.Replace(lines[8], `"status":"pass"`, `"status":"fail"`, 1),
		lines[9], lines[10], lines[11], "",
	}

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

// streamLines splits the recorded stream into the message lines synthetic
// sequences are built from. Every line stays real output's own; only names,
// statuses and order are rewritten, so each message keeps the shape Terraform
// actually wrote.
func streamLines(tb testing.TB) []string {
	tb.Helper()

	return strings.Split(string(recorded(tb)), "\n")
}

// renameRun returns a recorded run-block event line naming another run block,
// so a synthetic sequence can interleave several run blocks of one file.
func renameRun(line, to string) string {
	return strings.ReplaceAll(line, `"applied"`, `"`+to+`"`)
}

// failFastDiagnostic is one recorded assertion diagnostic with its detail
// replaced, so a synthetic burst's members are distinguishable.
func failFastDiagnostic(detail string) string {
	return `{"@level":"error","@message":"Error: Test assertion failed",` +
		`"@module":"terraform.ui","@testfile":"tests/apply.tftest.hcl","@testrun":"applied",` +
		`"@timestamp":"2026-10-24T16:54:05.070736+01:00","diagnostic":{"severity":"error",` +
		`"summary":"Test assertion failed","detail":"` + detail + `",` +
		`"range":{"filename":"tests/apply.tftest.hcl","start":{"line":5,"column":21,"byte":66},` +
		`"end":{"line":5,"column":41,"byte":86}},"snippet":{"context":"run \"applied\"",` +
		`"code":"    condition     = output.app == \"kept\"","start_line":5,` +
		`"highlight_start_offset":20,"highlight_end_offset":40}},"type":"diagnostic"}`
}

// parallelBurstStream builds a stream in which a second run block of the
// failing run's file is in flight when the arming run completes — the file is
// running its blocks in parallel — the given interrupt messages follow, and the
// arming run's two diagnostics arrive only afterwards, before the file's own
// teardown and completion. That is the order Terraform writes for a parallel
// file: the run's diagnostics trail every event the other blocks produce.
func parallelBurstStream(tb testing.TB, interrupt ...string) []byte {
	tb.Helper()

	lines := streamLines(tb)
	completed := strings.Replace(lines[4], `"status":"pass"`, `"status":"fail"`, 1)

	built := make([]string, 0, 5+len(interrupt)+5)
	built = append(built,
		lines[2],
		renameRun(lines[3], "sibling"),
		lines[3],
		completed,
		renameRun(completed, "sibling"),
	)
	built = append(built, interrupt...)
	built = append(built,
		failFastDiagnostic("first assertion"),
		failFastDiagnostic("second assertion"),
		lines[6],
		strings.Replace(lines[8], `"status":"pass"`, `"status":"fail"`, 1),
		"",
	)

	return []byte(strings.Join(built, "\n"))
}

// decodeArmingFail decodes a stream armed by its first failing run and reports
// the decoded record and how often the stop fired.
func decodeArmingFail(tb testing.TB, stream []byte) (TestResult, int) {
	tb.Helper()

	fires := 0

	result, err := decodeTestStreamStopping(bytes.NewReader(stream), func(run RunOutcome) bool {
		return run.Status == StatusFail
	}, func() {
		fires++
	})
	if err != nil {
		tb.Fatalf("decoding the stream: %v", err)
	}

	return result, fires
}

func TestAParallelFilesInterleavedEventsDoNotEndTheArmingRunsBurst(t *testing.T) {
	t.Parallel()

	// Another block of the same file starts between the arming completion and
	// the arming run's diagnostics: a start message proves nothing while the
	// file is still running blocks.
	lines := streamLines(t)
	result, fires := decodeArmingFail(t, parallelBurstStream(t, renameRun(lines[3], "third")))

	if !result.StoppedOnFail {
		t.Fatal("a parallel file that completed must record the stop")
	}

	if fires != 1 {
		t.Fatalf("the stop fired %d times, want once", fires)
	}

	if len(result.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %d decoded, want both of the arming run's", len(result.Diagnostics))
	}

	// The stop waits for the file's completion, so both completed blocks are
	// in the record: nothing the invocation would have run was cut short.
	if len(result.Runs) != 2 || result.Runs[0].Status != StatusFail {
		t.Fatalf("runs = %v, want the arming run and its sibling", result.Runs)
	}
}

func TestADelayedSiblingStartDoesNotEndTheArmingRunsBurst(t *testing.T) {
	t.Parallel()

	for _, complete := range []bool{false, true} {
		name := "without file completion"
		if complete {
			name = "with file completion"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := streamLines(t)
			built := []string{
				lines[2], lines[3],
				strings.Replace(lines[4], `"status":"pass"`, `"status":"fail"`, 1),
				renameRun(lines[3], "sibling"),
				renameRun(lines[4], "sibling"),
				renameRun(lines[5], "sibling"),
				lines[9], lines[14],
				failFastDiagnostic("first assertion"),
				lines[6], lines[7],
				failFastDiagnostic("second assertion"),
			}
			if complete {
				built = append(built,
					strings.Replace(lines[8], `"status":"pass"`, `"status":"fail"`, 1),
					lines[10], lines[11],
				)
			}

			result, fires := decodeArmingFail(t, []byte(strings.Join(built, "\n")))
			wantFires := 0
			if complete {
				wantFires = 1
			}
			if result.StoppedOnFail != complete || fires != wantFires {
				t.Fatalf("stopped = %t, fires = %d, want %t and %d", result.StoppedOnFail, fires, complete, wantFires)
			}
			if len(result.Diagnostics) != 2 ||
				result.Diagnostics[0].Detail != "first assertion" || result.Diagnostics[1].Detail != "second assertion" {
				t.Fatalf("diagnostics = %v, want both arming-run assertions", result.Diagnostics)
			}
			if len(result.Runs) != 2 || result.Runs[0].Status != StatusFail || result.Runs[1].Run != "sibling" {
				t.Fatalf("runs = %v, want the arming run and delayed sibling", result.Runs)
			}
		})
	}
}

func TestAnotherFilesEventsDoNotEndTheArmingRunsBurst(t *testing.T) {
	t.Parallel()

	lines := streamLines(t)

	// A later file's events arrive before the arming file's own completion.
	// They say nothing about the arming run's burst, so the stop stays queued
	// until the file that owns it completes.
	built := []string{
		lines[2],
		lines[3],
		strings.Replace(lines[4], `"status":"pass"`, `"status":"fail"`, 1),
		lines[9],
		lines[10],
		lines[11],
		failFastDiagnostic("first assertion"),
		failFastDiagnostic("second assertion"),
		strings.Replace(lines[8], `"status":"pass"`, `"status":"fail"`, 1),
		"",
	}

	result, fires := decodeArmingFail(t, []byte(strings.Join(built, "\n")))

	if !result.StoppedOnFail {
		t.Fatal("the armed file's completion must record the stop")
	}

	if fires != 1 {
		t.Fatalf("the stop fired %d times, want once", fires)
	}

	if len(result.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %d decoded, want both of the arming run's", len(result.Diagnostics))
	}
}
