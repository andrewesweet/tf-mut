package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

const (
	reporterTerminal   = "terminal"
	reporterHTML       = "html"
	failureModeTrip    = "trip"
	failureModeTimeout = "timeout"
	tripFirstFailure   = "First failure: terraform validate could not be executed"
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

	binary := dyingTerraform(t)
	for _, jobs := range []int{1, testJobs} {
		t.Run(fmt.Sprintf("jobs-%d", jobs), func(t *testing.T) {
			t.Parallel()

			assertFailingDependencyStopsAtThreshold(t, jobs, binary)
		})
	}
}

func assertFailingDependencyStopsAtThreshold(t *testing.T, jobs int, binary string) {
	t.Helper()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.Jobs = jobs
	config.TerraformBinary = binary

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	maximumFailures := engine.DefaultFailureTripThreshold + jobs - 1
	if len(result.Errors) < engine.DefaultFailureTripThreshold ||
		len(result.Errors) > maximumFailures {
		t.Fatalf("operational failures = %d, want between %d and %d: %v",
			len(result.Errors), engine.DefaultFailureTripThreshold,
			maximumFailures, result.Errors)
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
	assertTripProvenance(t, result, report.SelectionFull)
}

func TestATripPreservesSelectionAndExecutionProvenance(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{report.SelectionFull, report.SelectionSince, report.SelectionSample} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			module := tripModule(t, 10)
			config := baseConfig(t, module)
			config.Jobs = 1
			config.NoCache = true
			config.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
			config.TerraformBinary = dyingTerraform(t)

			switch mode {
			case report.SelectionSince:
				git(t, module, "init", "--quiet", "--initial-branch=main")
				git(t, module, "add", "--all")
				commit(t, module, "initial")
				path := filepath.Join(module, "main.tf")
				testsupport.WriteFile(t, path, readFile(t, path)+"\n")
				config.Since = sinceHead
			case report.SelectionSample:
				config.HasSample = true
				config.SamplePercent = 80
				config.SampleSeed = 17
			default:
				// Full selection uses the configuration unchanged.
			}

			result := runWith(t, config)
			assertTripWarning(t, result)
			assertTripProvenance(t, result, mode)
		})
	}
}

func TestATripKeepsCachedVerdictsDistinctFromUnexecutedMutants(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, tripModule(t, 10))
	config.Jobs = 1
	config.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	config.TerraformBinary = dyingTerraform(t, "m000[1-9]-")

	first := runWith(t, config)
	if first.Population.Cached != 0 || first.Count(report.Killed) != 1 {
		t.Fatalf("want one fresh kill to seed the cache: %+v", first.Population)
	}

	second := runWith(t, config)
	if second.Population.Cached != 1 {
		t.Fatalf("cached = %d, want the first run's one verdict", second.Population.Cached)
	}

	assertTripWarning(t, second)
	assertTripProvenance(t, second, report.SelectionFull)
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

func TestATripWithEveryMutantDispatchedStillMarksTheScoreIncomplete(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, tripModule(t, 6))
	config.Jobs = 1
	config.NoCache = true
	config.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	config.TerraformBinary = dyingTerraform(t, "m0001-", "m0002-", "m0003-", "m0004-", "m0005-")

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) != 6 || len(result.Errors) != 5 ||
		result.Metrics.Scored != 1 || result.Metrics.Counts[report.Killed] != 1 {
		t.Fatalf("want one kill followed by five operational failures: %+v", result)
	}

	for _, mutant := range result.Mutants {
		if mutant.Telemetry == nil {
			t.Fatalf("mutant %s was not dispatched", mutant.ID)
		}
	}

	if !result.Metrics.Incomplete {
		t.Fatalf("a trip with no undispatched mutants left a complete score: %+v", result.Metrics)
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitOperational { //nolint:exhaustruct // no gate.
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	assertTripWarning(t, result)
	assertTripProvenance(t, result, report.SelectionFull)

	for name, write := range map[string]func(io.Writer, report.Report) error{
		reporterTerminal: report.WriteTerminal,
		reporterHTML:     report.WriteHTML,
		reporterMarkdown: report.WriteMarkdown,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var rendered strings.Builder
			if err := write(&rendered, result); err != nil {
				t.Fatalf("rendering: %v", err)
			}

			if !strings.Contains(strings.ToLower(rendered.String()), "operational-failure trip stopped execution") {
				t.Fatal("the incomplete score explanation omits the operational-failure trip")
			}
		})
	}
}

func TestABaselineCheckReportsUnobservedMutantsWithoutStaleness(t *testing.T) {
	t.Parallel()

	module := tripModule(t, 10)
	outputs := make([]string, 10)
	for index := range outputs {
		outputs[index] = fmt.Sprintf("output.value%02d", index)
	}
	testsupport.WriteFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), "run \"weak\" {\n  command = plan\n\n"+
		"  assert {\n    condition     = length(["+strings.Join(outputs, ", ")+"]) == 10\n"+
		"    error_message = \"all outputs must exist\"\n  }\n}\n")

	adopt := baseConfig(t, module)
	adopt.NoCache = true
	adopt.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	adopt.WriteBaseline = true
	accepted := runWith(t, adopt)
	ids := findingIDs(accepted)
	if len(ids) != 10 {
		t.Fatalf("want ten accepted survivors, got %v", ids)
	}

	for _, mode := range []string{failureModeTrip, failureModeTimeout} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			config := adopt
			config.WriteBaseline = false
			config.FailOnNew = true
			config.Jobs = 1
			if mode == failureModeTrip {
				config.TerraformBinary = dyingTerraform(t)
			} else {
				config.TimeoutFactor = 0.0001
				config.TimeoutFloor = time.Millisecond
			}

			result := runWith(t, config)
			if mode == failureModeTrip {
				assertTripWarning(t, result)
			} else if result.Count(report.Timeout) == 0 {
				t.Fatal("the run observed no timeout")
			}

			assertUnobservedBaseline(t, result, ids)
		})
	}
}

func assertUnobservedBaseline(t *testing.T, result report.Report, ids []string) {
	t.Helper()

	baseline := result.Gates.Baseline
	if baseline == nil || baseline.StalenessReported || len(baseline.Stale) != 0 {
		t.Fatalf("an unobserved population reported staleness: %+v", baseline)
	}
	if !slices.Equal(baseline.Unobserved, ids) {
		t.Fatalf("unobserved = %v, want %v", baseline.Unobserved, ids)
	}
	if !result.Gates.FailOnNew.Partial || result.Gates.FailOnNew.Scope != gateScopeSelected {
		t.Fatalf("an unobserved population claims a full gate: %+v", result.Gates.FailOnNew)
	}

	for _, mutant := range result.Mutants {
		if mutant.Provenance == nil || mutant.Provenance.BaselineStatus != "unobserved" {
			t.Fatalf("mutant %s is not labelled unobserved: %+v", mutant.ID, mutant.Provenance)
		}
	}
}

//nolint:paralleltest // Swaps the package-level progress interval, which is not safe in parallel.
func TestInFlightVerdictsAndFailuresPreserveTheTripEvidence(t *testing.T) {
	releases := t.TempDir()
	path := filepath.Join(t.TempDir(), "terraform-trip-evidence")
	script := `#!/usr/bin/env bash
if [ "$2" = "test" ] || [ "$2" = "validate" ]; then
  case "$1" in
    */m0000-*)
      while [ ! -f "$TF_MUT_TRIP_RELEASES/done-5" ]; do sleep 0.01; done
      ;;
    */m0002-*|*/m000[7-9]-*)
      while [ ! -f "$TF_MUT_TRIP_RELEASES/done-6" ]; do sleep 0.01; done
      if [ "$2" = "validate" ]; then printf 'later'; fi
      exit 137
      ;;
    */m[0-9][0-9][0-9][0-9]-*)
      if [ "$2" = "validate" ]; then printf 'earlier'; fi
      exit 137
      ;;
  esac
fi
exec terraform "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the wrapper must run.
		t.Fatalf("writing terraform wrapper: %v", err)
	}

	config := baseConfig(t, tripModule(t, 10))
	config.Jobs = 3
	config.NoCache = true
	config.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	config.TerraformBinary = path
	config.Env = append(config.Env, "TF_MUT_TRIP_RELEASES="+releases)
	config.Progress = tripReleaseWriter{directory: releases}
	engine.SetProgressInterval(t, time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	result, err := engine.Run(ctx, config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Metrics.Counts[report.Killed] != 1 {
		t.Fatalf("the in-flight successful evaluation was not published: %+v", result.Metrics)
	}

	firstFailure := ""
	lateFailures := 0
	for _, failure := range result.Errors {
		switch {
		case strings.Contains(failure.Message, "invalid character 'e'"):
			firstFailure = failure.Message
		case strings.Contains(failure.Message, "invalid character 'l'"):
			lateFailures++
		default:
			t.Fatalf("unexpected failure: %+v", failure)
		}
	}

	if firstFailure == "" || lateFailures == 0 {
		t.Fatalf("want both triggering and late failures: %v", result.Errors)
	}

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "run tripped after 5 consecutive operational failures") &&
			strings.Contains(warning, "First failure: "+firstFailure) {
			return
		}
	}

	t.Fatalf("the trip lost its triggering evidence: %v", result.Warnings)
}

type tripReleaseWriter struct {
	directory string
}

func (w tripReleaseWriter) Write(content []byte) (int, error) {
	done, total := 0, 0
	if _, err := fmt.Sscanf(string(content), "tf-mut: %d/%d mutants,", &done, &total); err != nil {
		return 0, err
	}

	for _, threshold := range []int{5, 6} {
		if done >= threshold {
			path := filepath.Join(w.directory, fmt.Sprintf("done-%d", threshold))
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return 0, err
			}
		}
	}

	return len(content), nil
}

func tripModule(t *testing.T, count int) string {
	t.Helper()

	module := t.TempDir()
	source := strings.Builder{}
	outputs := make([]string, count)
	for index := range count {
		fmt.Fprintf(&source, "output \"value%02d\" {\n  value = true\n}\n\n", index)
		outputs[index] = fmt.Sprintf("output.value%02d", index)
	}
	testsupport.WriteFile(t, filepath.Join(module, "main.tf"), source.String())

	tests := filepath.Join(module, "tests")
	if err := os.Mkdir(tests, 0o750); err != nil {
		t.Fatalf("creating tests: %v", err)
	}
	testsupport.WriteFile(t, filepath.Join(tests, "unit.tftest.hcl"), "run \"defaults\" {\n  command = plan\n\n"+
		"  assert {\n    condition     = alltrue(["+strings.Join(outputs, ", ")+"])\n"+
		"    error_message = \"all values must remain true\"\n  }\n}\n")

	return module
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
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

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

	binary := dyingTerraform(t)

	for _, command := range []report.Command{report.CommandRun, report.CommandSuggest, report.CommandCurate} {
		t.Run(string(command), func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "skeleton")
			config := baseConfig(t, module)
			config.Jobs = testJobs
			config.WriteBaseline = true
			config.TerraformBinary = binary

			var request engine.Request = config
			if command == report.CommandSuggest {
				request = engine.SuggestRequest{
					Common: config.Common, Population: config.Population, Gate: config.Gate,
				}
			}
			if command == report.CommandCurate {
				request = engine.CurateRequest{Common: config.Common, Gate: config.Gate}
			}

			_, err := engine.Run(t.Context(), request)
			if !errors.Is(err, engine.ErrBaselineUnobserved) {
				t.Fatalf("run error = %v, want %v", err, engine.ErrBaselineUnobserved)
			}

			for _, evidence := range []string{
				"warning: run tripped after 5 consecutive operational failures",
				"mutant(s) were left unexecuted and the mutation score is marked incomplete",
				tripFirstFailure,
			} {
				if !strings.Contains(err.Error(), evidence) {
					t.Fatalf("the refusal lost trip evidence %q: %v", evidence, err)
				}
			}

			if _, statErr := os.Stat(filepath.Join(module, engine.DefaultBaselineName)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("the tripped run wrote a baseline: %v", statErr)
			}
		})
	}
}

func TestPostFinishAuthorityRefusalsRetainTheTripDiagnostic(t *testing.T) {
	t.Parallel()

	binary := dyingTerraform(t)

	for _, command := range []report.Command{report.CommandCurate, report.CommandCharacterise} {
		t.Run(string(command), func(t *testing.T) {
			t.Parallel()

			var request engine.Request
			refusal := engine.ErrCuratePopulation
			if command == report.CommandCurate {
				config := curateRequest(t, copyFixture(t, "skeleton"))
				config.NoCache = true
				config.Jobs = 1
				config.TerraformBinary = binary
				request = config
			} else {
				config := characteriseRequest(t, copyFixture(t, untestedBranchesFixture))
				config.UntilDry = true
				config.Jobs = 1
				config.TerraformBinary = binary
				request = config
				refusal = engine.ErrUntilDryPopulation
			}

			_, err := engine.Run(t.Context(), request)
			if !errors.Is(err, refusal) {
				t.Fatalf("error = %v, want %v", err, refusal)
			}
			for _, evidence := range []string{
				"warning: run tripped after",
				"consecutive operational failures",
				"the mutation score is marked incomplete",
				tripFirstFailure,
			} {
				if strings.Count(err.Error(), evidence) != 1 {
					t.Fatalf("the refusal must carry trip evidence %q once: %v", evidence, err)
				}
			}
		})
	}
}

func TestATrippedSurvivorSelectionRefusalRetainsTheTripDiagnostic(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, copyFixture(t, "skeleton"))
	config.Jobs = 1
	config.NoCache = true
	control := runWith(t, config)

	id := ""
	for index := len(control.Mutants) - 1; index > engine.DefaultFailureTripThreshold+config.Jobs; index-- {
		if control.Mutants[index].State == report.Survived {
			id = control.Mutants[index].ID
			break
		}
	}
	if id == "" {
		t.Fatal("the control needs a survivor after the trip point")
	}

	config.TerraformBinary = dyingTerraform(t)
	request := engine.SuggestRequest{
		Common: config.Common, Population: config.Population, Gate: config.Gate,
		NoCache: true, SurvivorIDs: []string{id},
	}
	_, err := engine.Run(t.Context(), request)
	if !errors.Is(err, engine.ErrSurvivorSelection) {
		t.Fatalf("error = %v, want %v", err, engine.ErrSurvivorSelection)
	}
	for _, evidence := range []string{
		id + " is not a survivor of this population",
		"the report it came from is stale, so re-run before selecting again",
		"warning: run tripped after 5 consecutive operational failures",
		"the mutation score is marked incomplete",
		tripFirstFailure,
	} {
		if strings.Count(err.Error(), evidence) != 1 {
			t.Fatalf("the refusal must carry evidence %q once: %v", evidence, err)
		}
	}
}

// assertTripWarning checks the run warned about the trip and named the first
// failure's message. The dying wrapper makes every failure's message
// identical, so the first failure's message is every failure's message.
func assertTripWarning(t *testing.T, result report.Report) {
	t.Helper()

	if count := strings.Count(strings.Join(result.Warnings, "\n"), "run tripped after"); count != 1 {
		t.Fatalf("trip warnings = %d, want exactly one: %v", count, result.Warnings)
	}

	unexecuted := 0
	for _, mutant := range result.Mutants {
		if mutant.State == report.Pending && mutant.Telemetry == nil {
			unexecuted++
		}
	}

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "consecutive operational failures") &&
			strings.Contains(warning, tripFirstFailure) {
			if !strings.Contains(warning, fmt.Sprintf("%d mutant(s) were left unexecuted", unexecuted)) ||
				!strings.Contains(warning, "the mutation score is marked incomplete") ||
				strings.Contains(warning, "cached") {
				t.Fatalf("trip warning has incorrect evidence or promises caching: %s", warning)
			}

			return
		}
	}

	t.Fatalf("no warning names the trip and the first failure: %v", result.Warnings)
}

func assertTripProvenance(t *testing.T, result report.Report, selection string) {
	t.Helper()

	fresh, cached, unexecuted := 0, 0, 0
	for _, mutant := range result.Mutants {
		provenance := mutant.Provenance
		if provenance == nil || provenance.Selection != selection || provenance.Reason == "" {
			t.Fatalf("mutant %s lost selection provenance: %+v", mutant.ID, provenance)
		}

		switch provenance.Execution {
		case report.ExecutionFresh:
			fresh++
			if mutant.Telemetry == nil {
				t.Fatalf("mutant %s claims fresh execution without telemetry", mutant.ID)
			}
		case report.ExecutionCached:
			cached++
			if mutant.Telemetry != nil || provenance.CacheKey == "" {
				t.Fatalf("mutant %s lost cached execution provenance", mutant.ID)
			}
		case report.ExecutionUnexecuted:
			unexecuted++
			if mutant.State != report.Pending || mutant.Telemetry != nil || provenance.CacheKey != "" {
				t.Fatalf("undispatched mutant %s claims an execution: %+v", mutant.ID, mutant)
			}
		default:
			t.Fatalf("mutant %s has unknown execution provenance %q", mutant.ID, provenance.Execution)
		}
	}

	if result.Population.Fresh != fresh || result.Population.Cached != cached ||
		result.Population.Selected != fresh+cached+unexecuted {
		t.Fatalf("population split disagrees with execution provenance: %+v", result.Population)
	}

	var encoded strings.Builder
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("writing JSON: %v", err)
	}
	document := any(nil)
	if err := json.Unmarshal([]byte(encoded.String()), &document); err != nil {
		t.Fatalf("decoding JSON: %v", err)
	}
	schema := loadPublishedSchema(t)
	if problems := validateAgainst(schema, schema, document, "$"); len(problems) > 0 {
		t.Fatalf("trip report violates the published schema: %v", problems)
	}
}

func assertNoTripWarning(t *testing.T, result report.Report) {
	t.Helper()

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "consecutive operational failures") {
			t.Fatalf("run tripped but must not have: %v", result.Warnings)
		}
	}
}
