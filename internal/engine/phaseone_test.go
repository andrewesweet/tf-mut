package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

const (
	phaseOneFirstFile          = "tests/first.tftest.hcl"
	phaseOneNonNullCondition   = "output.value != null"
	phaseOneExitFailure        = "exit 1"
	phaseOneExitSuccess        = "exit 0"
	phaseOneNonzeroExitMessage = "terraform test exited 1"
)

func phaseOneModule(t *testing.T, condition string) string {
	t.Helper()

	module := t.TempDir()
	testsupport.WriteFile(t, filepath.Join(module, mainFile), `output "value" {
  value = true
}
`)
	if err := os.Mkdir(filepath.Join(module, "tests"), 0o750); err != nil {
		t.Fatalf("creating tests: %v", err)
	}

	for _, test := range []struct{ file, condition string }{
		{phaseOneFirstFile, condition},
		{"tests/second.tftest.hcl", phaseOneNonNullCondition},
	} {
		testsupport.WriteFile(t, filepath.Join(module, test.file), fmt.Sprintf(`run "planned" {
  command = plan

  assert {
    condition     = %s
    error_message = "value must satisfy the condition"
  }
}
`, test.condition))
	}

	return module
}

func phaseOneTerraform(t *testing.T, filter, completion string) string {
	t.Helper()

	if filter != "" {
		filter = "-filter=" + filter
	}

	// The mutant's execution is verbose under the single-phase contract, so
	// the doctoring applies to the mutant's one invocation. The baseline runs
	// in the warm workspace, outside the case's sandbox pattern, and passes
	// through untouched.
	script := `#!/usr/bin/env bash
if [ "$2" = test ]; then
  case "$1" in
    */m[0-9][0-9][0-9][0-9]-*)
      terraform "$@" ` + filter + `
      ` + completion + `
      ;;
  esac
fi
exec terraform "$@"
`
	path := filepath.Join(t.TempDir(), "terraform-phase-one")
	testsupport.WriteExecutable(t, path, script)

	return path
}

func TestPhaseOneTruncationIsAnExecutionError(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		filter     string
		completion string
		selection  []string
		wantRuns   int
		message    string
	}{
		{"nonzero exit with full count", "", phaseOneExitFailure, nil, 2, phaseOneNonzeroExitMessage},
		{"nonzero exit with short count", phaseOneFirstFile, phaseOneExitFailure, nil, 1, phaseOneNonzeroExitMessage},
		{"crash exit with passing runs", phaseOneFirstFile, "exit 11", nil, 1, "terraform test exited 11"},
		{
			"zero exit with short count", phaseOneFirstFile, phaseOneExitSuccess,
			nil, 1, "only 1 of 2 baseline run block(s) executed",
		},
		{"filtered nonzero exit", "", phaseOneExitFailure, []string{phaseOneFirstFile}, 1, phaseOneNonzeroExitMessage},
		{"zero executed runs", "tests/missing.tftest.hcl", phaseOneExitSuccess, nil, 0, "no run block executed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := baseConfig(t, phaseOneModule(t, phaseOneNonNullCondition))
			request.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
			request.TestSelection = test.selection
			request.TerraformBinary = phaseOneTerraform(t, test.filter, test.completion)

			for range 2 {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}

				assertPhaseOneExecutionError(t, result, test.wantRuns, test.message)
			}
		})
	}
}

func assertPhaseOneExecutionError(t *testing.T, result report.Report, wantRuns int, message string) {
	t.Helper()

	if len(result.Mutants) != 1 || len(result.Errors) != 1 {
		t.Fatalf("want one mutant and one execution error: %+v", result)
	}

	mutant := result.Mutants[0]
	failure := result.Errors[0]
	if failure.MutantID != mutant.ID || failure.Site != mutant.Site ||
		!strings.Contains(failure.Message, message) {
		t.Fatalf("execution error = %+v, want %q for %s", failure, message, mutant.ID)
	}

	if mutant.State != report.Pending || mutant.Verdict != nil || mutant.ExecutedRuns != wantRuns {
		t.Fatalf("a refused stream produced a verdict or lost its run count: %+v", mutant)
	}

	for _, run := range mutant.Runs {
		if run.Phase != 1 || run.Status != "pass" {
			t.Fatalf("want only passing phase-one runs: %+v", mutant.Runs)
		}
	}

	if result.Metrics.Scored != 0 || !result.Metrics.Incomplete || result.Population.Cached != 0 {
		t.Fatalf("an execution error was scored or cached: %+v, %+v", result.Metrics, result.Population)
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}
}

func TestPhaseOneTruncationPreservesVerdicts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		condition  string
		filter     string
		completion string
		wantRuns   int
		state      report.State
		validated  bool
	}{
		{"full passing suite", phaseOneNonNullCondition, "", phaseOneExitSuccess, 2, report.Survived, false},
		{"failing short suite", "output.value", phaseOneFirstFile, phaseOneExitFailure, 1, report.Killed, false},
		{
			name:       "erroring short suite",
			condition:  `output.value ? true : tonumber("invalid") == 0`,
			filter:     phaseOneFirstFile,
			completion: phaseOneExitFailure,
			wantRuns:   1,
			state:      report.KilledByError,
			validated:  true,
		},
		{"timed out short suite", phaseOneNonNullCondition, phaseOneFirstFile, "sleep 300", 1, report.Timeout, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := baseConfig(t, phaseOneModule(t, test.condition))
			request.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
			request.TerraformBinary = phaseOneTerraform(t, test.filter, test.completion)
			request.NoCache = true
			if test.state == report.Timeout {
				request.TimeoutFactor = 0.0001
				request.TimeoutFloor = 2 * time.Second
			}

			result := runWith(t, request)
			if len(result.Errors) != 0 || len(result.Mutants) != 1 || result.Metrics.Scored != 1 {
				t.Fatalf("want one scored verdict without execution errors: %+v", result)
			}

			mutant := result.Mutants[0]
			if mutant.State != test.state || mutant.ExecutedRuns != test.wantRuns || mutant.Validated != test.validated {
				t.Fatalf("want %s over %d runs, validated %t: %+v", test.state, test.wantRuns, test.validated, mutant)
			}

			if mutant.DeadlineReached != (test.state == report.Timeout) {
				t.Fatalf("deadline evidence disagrees with %s: %+v", test.state, mutant)
			}
		})
	}
}

// TestAFilteredShortStreamIsFingerprintedFromItsOwnRun holds the filtered
// selection's single-phase contract: a selection that runs fewer run blocks
// than the unfiltered baseline fingerprinted is not a truncation, so the
// mutant's passing stream is fingerprinted and the comparison runs on it.
// The comparison refuses — the baseline fingerprinted a run block the filtered
// stream never delivered, the latent filtered-run refusal recorded as a
// follow-up in docs/research/23-l2-single-verbose-phase.md — and the refusal
// is the fingerprint one, not the phase-one truncation refusal, which is what
// proves the stream reached the comparison at all.
func TestAFilteredShortStreamIsFingerprintedFromItsOwnRun(t *testing.T) {
	t.Parallel()

	request := baseConfig(t, phaseOneModule(t, phaseOneNonNullCondition))
	request.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
	request.TestSelection = []string{phaseOneFirstFile}
	request.NoCache = true

	result := runWith(t, request)
	if result.Baseline.Runs != 2 || len(result.Mutants) != 1 {
		t.Fatalf("want two baseline runs and one mutant: %+v", result)
	}

	mutant := result.Mutants[0]
	if mutant.ExecutedRuns != 1 || mutant.Validated || mutant.DeadlineReached {
		t.Fatalf("want one passing phase-one run without validation or a deadline: %+v", mutant)
	}

	for _, run := range mutant.Runs {
		if run.Phase != 1 {
			t.Fatalf("the filtered stream was re-run for its fingerprint: %+v", mutant.Runs)
		}
	}

	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message,
		"the fingerprint run produced no payload for") {
		t.Fatalf("want the fingerprint refusal the comparison raised over the mutant's own stream: %+v",
			result.Errors)
	}
}
