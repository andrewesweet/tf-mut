package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// dyingTerraform wraps the real binary and makes it die for the mutant
// sandboxes whose described index matches one of the markers — and, with no
// markers, for every mutant sandbox. Death covers both `test` and `validate`
// on the sandbox, the observable shape of a dependency that fails per
// invocation: a Terraform that cannot start, a provider cache left in the
// text-file-busy state, a full disk. The warm-up directory is never a mutant
// sandbox, so the baseline still runs and the run reaches execution.
func dyingTerraform(t *testing.T, markers ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "terraform-dying")

	pattern := "*/m[0-9][0-9][0-9][0-9]-*"
	if len(markers) > 0 {
		alternatives := make([]string, len(markers))
		for i, marker := range markers {
			alternatives[i] = "*" + marker + "*"
		}

		pattern = strings.Join(alternatives, "|")
	}

	script := `#!/usr/bin/env bash
if [ "$2" = "test" ] || [ "$2" = "validate" ]; then
  case "$1" in
    ` + pattern + `) exit 137 ;;
  esac
fi
exec terraform "$@"
`

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the wrapper must run.
		t.Fatalf("writing terraform wrapper: %v", err)
	}

	return path
}

// TestAFailingDependencyStopsTheRunAtTheThreshold is the trip the release-it
// review ticketed: a dependency that fails for every mutant is reported after
// a few failures instead of after the whole population.
func TestAFailingDependencyStopsTheRunAtTheThreshold(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	config.TerraformBinary = dyingTerraform(t)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The trip fires at the fifth consecutive failure. The workers already
	// holding a mutant finish it, so the executed count is the threshold plus
	// at most one in-flight mutant per worker; everything after stops.
	if len(result.Errors) < engine.DefaultFailureTripThreshold ||
		len(result.Errors) > engine.DefaultFailureTripThreshold+testJobs {
		t.Fatalf("operational failures = %d, want between %d and %d: %v",
			len(result.Errors), engine.DefaultFailureTripThreshold,
			engine.DefaultFailureTripThreshold+testJobs, result.Errors)
	}

	executed := 0
	unexecuted := 0

	for _, mutant := range result.Mutants {
		if mutant.Telemetry != nil {
			executed++
		}

		// An operationally failed row keeps the pending state beside its
		// telemetry; a row with neither ran nowhere — it was never dispatched.
		if mutant.State == report.Pending && mutant.Telemetry == nil {
			unexecuted++
		}
	}

	if executed >= len(result.Mutants) {
		t.Fatalf("executed %d of %d mutants: the run did not stop early", executed, len(result.Mutants))
	}

	if unexecuted == 0 {
		t.Fatal("no unexecuted mutants: the remainder was not left unexecuted")
	}

	if !result.Metrics.Incomplete {
		t.Fatalf("metrics incomplete = false, want true: %+v", result.Metrics)
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitOperational { //nolint:exhaustruct // no gate.
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	assertTripWarning(t, result)
}

// TestTheTripThresholdCapsAtThePendingPopulation covers the clause that makes
// the threshold reachable on a small population: with four pending mutants and
// the default threshold of five, the whole population failing is the trip.
func TestTheTripThresholdCapsAtThePendingPopulation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	// The smoke tier of the skeleton fixture generates four mutants, all
	// executable: fewer than the default threshold of five.
	config.Tier = mutation.TierSmoke
	config.TerraformBinary = dyingTerraform(t)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) != 4 {
		t.Fatalf("operational failures = %d, want 4: %v", len(result.Errors), result.Errors)
	}

	assertTripWarning(t, result)

	if !result.Metrics.Incomplete {
		t.Fatalf("metrics incomplete = false, want true: %+v", result.Metrics)
	}
}

// TestRaisingTheThresholdKeepsARunGoingThroughItsFailures pins the threshold
// as a setting: against the same all-failing population, a raised threshold
// trips later than the default — eight to ten failures instead of five to
// seven — so a CI with a known-flaky mirror can raise it.
func TestRaisingTheThresholdKeepsARunGoingThroughItsFailures(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	// The full skeleton tier has ten executable mutants.
	config.FailureTripThreshold = 8
	config.TerraformBinary = dyingTerraform(t)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) < 8 || len(result.Errors) > 10 {
		t.Fatalf("operational failures = %d, want between 8 and 10: %v", len(result.Errors), result.Errors)
	}

	assertTripWarning(t, result)
}

// TestInterleavedFailuresAndVerdictsNeverTrip is the honesty case: the count
// is consecutive, so failures separated by verdicts never reach the threshold
// even when their total does.
//
// The failing indices are described-order positions in the applicability-
// matrix fixture that its operators leave executable, each separated from the
// next by at least two executable neighbours. With two workers a mutant cannot
// be dispatched until both its neighbours were received, and a worker
// publishes before it receives, so a verdict is always published between two
// of these failures: the consecutive count never leaves one. If the fixture's
// executable indices move — the applicability-matrix audit forces any such
// change through the matrix test — recompute them from a run report.
func TestInterleavedFailuresAndVerdictsNeverTrip(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "operators")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	config.TerraformBinary = dyingTerraform(t, "m0004-", "m0008-", "m0012-", "m0016-", "m0020-")

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) != 5 {
		t.Fatalf("operational failures = %d, want 5: %v", len(result.Errors), result.Errors)
	}

	assertNoTripWarning(t, result)

	if result.Metrics.Incomplete {
		t.Fatalf("metrics incomplete = true, want false: %+v", result.Metrics)
	}

	for _, mutant := range result.Mutants {
		if mutant.State == report.Pending && mutant.Telemetry == nil {
			t.Fatalf("mutant %s never ran: an interleaved run must execute its whole population", mutant.ID)
		}
	}
}

// TestATrippedRunWritesNoBaseline: a tripped population is not fully
// observed, so the acceptance list — the record of what a run observed — must
// refuse the write.
func TestATrippedRunWritesNoBaseline(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = testJobs
	config.WriteBaseline = true
	config.TerraformBinary = dyingTerraform(t)

	_, err := engine.Run(t.Context(), config)
	if err == nil {
		t.Fatal("the tripped run wrote a baseline")
	}

	if !errors.Is(err, engine.ErrBaselineUnobserved) {
		t.Fatalf("run error = %v, want %v", err, engine.ErrBaselineUnobserved)
	}

	if _, statErr := os.Stat(filepath.Join(module, engine.DefaultBaselineName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the tripped run wrote a baseline: %v", statErr)
	}
}

// assertTripWarning checks the run warned about the trip and named the first
// failure's message. The dying wrapper makes every failure's message
// identical, so the first failure's message is every failure's message.
func assertTripWarning(t *testing.T, result report.Report) {
	t.Helper()

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "consecutive operational failures") &&
			strings.Contains(warning, "terraform validate could not be executed") {
			return
		}
	}

	t.Fatalf("no warning names the trip and the first failure: %v", result.Warnings)
}

func assertNoTripWarning(t *testing.T, result report.Report) {
	t.Helper()

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "consecutive operational failures") {
			t.Fatalf("run tripped but must not have: %v", result.Warnings)
		}
	}
}
