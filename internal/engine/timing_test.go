package engine_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestBaselinePublishesPerFileTiming asserts the baseline block carries one
// timing entry per test file the baseline executed, measured from the stream
// the baseline run already produced.
func TestBaselinePublishesPerFileTiming(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	files := result.Baseline.Files
	if len(files) != 1 {
		t.Fatalf("baseline published %d file timings, want 1", len(files))
	}

	file := files[0]
	if file.File != "tests/unit.tftest.hcl" {
		t.Errorf("file timing names %q, want tests/unit.tftest.hcl", file.File)
	}

	if file.Runs != 2 {
		t.Errorf("file timing counts %d runs, want the file's 2", file.Runs)
	}

	if file.ElapsedMS == nil {
		t.Fatal("file timing carries no elapsed time")
	}

	if *file.ElapsedMS < 0 {
		t.Errorf("file timing elapsed %dms is negative", *file.ElapsedMS)
	}

	counted := 0
	for _, entry := range files {
		counted += entry.Runs
	}

	if counted != result.Baseline.Runs {
		t.Errorf("file timings count %d runs, want the baseline's %d", counted, result.Baseline.Runs)
	}
}

// TestRunOutcomesCarryTheirWallTime asserts every executed run block's
// published outcome carries the wall time the stream's own timestamps
// measure, and that a run block Terraform skipped — completed without ever
// starting, after an earlier error — carries none.
func TestRunOutcomesCarryTheirWallTime(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	observed := 0
	for _, mutant := range result.Mutants {
		for _, run := range mutant.Runs {
			if run.Status == "skip" {
				if run.ElapsedMS != nil {
					t.Errorf("skipped run %s/%s carries elapsed %dms, want none",
						run.File, run.Run, *run.ElapsedMS)
				}

				continue
			}

			if run.ElapsedMS == nil {
				t.Errorf("executed run %s/%s (%s) carries no elapsed time", run.File, run.Run, run.Status)

				continue
			}

			if *run.ElapsedMS < 0 {
				t.Errorf("run %s/%s elapsed %dms is negative", run.File, run.Run, *run.ElapsedMS)
			}

			observed++
		}
	}

	if observed == 0 {
		t.Fatal("no mutant published an executed run outcome")
	}
}

// TestTelemetryCarriesStartTimestamps asserts the phase and mutant telemetry
// carry their start instants, parseable as RFC 3339 in UTC, each no later
// than its own duration's end.
func TestTelemetryCarriesStartTimestamps(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	if result.Telemetry == nil {
		t.Fatal("the run published no telemetry")
	}

	if len(result.Telemetry.Phases) == 0 {
		t.Fatal("the run telemetry recorded no phases")
	}

	for _, phase := range result.Telemetry.Phases {
		started, err := time.Parse(time.RFC3339Nano, phase.StartedAt)
		if err != nil {
			t.Fatalf("phase %s started_at %q is not RFC 3339: %v", phase.Phase, phase.StartedAt, err)
		}

		if started.Location() != time.UTC {
			t.Errorf("phase %s started_at %q is not UTC", phase.Phase, phase.StartedAt)
		}
	}

	for _, mutant := range result.Mutants {
		if mutant.Telemetry == nil {
			continue
		}

		started, err := time.Parse(time.RFC3339Nano, mutant.Telemetry.StartedAt)
		if err != nil {
			t.Fatalf("mutant %s started_at %q is not RFC 3339: %v",
				mutant.ID, mutant.Telemetry.StartedAt, err)
		}

		if started.Location() != time.UTC {
			t.Errorf("mutant %s started_at %q is not UTC", mutant.ID, mutant.Telemetry.StartedAt)
		}
	}
}

// TestSchemaPublishesTheTimingFields asserts the emitted JSON document
// carries the 2.16.0 timing fields on the wire, spelled as the schema says.
func TestSchemaPublishesTheTimingFields(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	if result.SchemaVersion != report.SchemaVersion {
		t.Fatalf("schema stamp %q, want %q", result.SchemaVersion, report.SchemaVersion)
	}

	rendered := renderJSON(t, result)

	for _, member := range []string{
		`"elapsed_ms"`,
		`"started_at"`,
		`"files"`,
	} {
		if !strings.Contains(rendered, member) {
			t.Errorf("the rendered report carries no %s", member)
		}
	}
}

// renderJSON marshals the report the way the JSON reporter does.
func renderJSON(t *testing.T, result report.Report) string {
	t.Helper()

	var rendered bytes.Buffer
	if err := report.WriteJSON(&rendered, result); err != nil {
		t.Fatalf("rendering the report: %v", err)
	}

	return rendered.String()
}
