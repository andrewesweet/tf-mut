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

const phaseOneFirstFile = "tests/first.tftest.hcl"

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
		{"tests/second.tftest.hcl", "output.value != null"},
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

	script := `#!/usr/bin/env bash
if [ "$2" = test ]; then
  case "$1" in
    */m[0-9][0-9][0-9][0-9]-*)
      for argument in "$@"; do
        if [ "$argument" = -verbose ]; then exec terraform "$@"; fi
      done
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
		{"nonzero exit with full count", "", "exit 1", nil, 2, "terraform test exited 1"},
		{"nonzero exit with short count", phaseOneFirstFile, "exit 1", nil, 1, "terraform test exited 1"},
		{"crash exit with passing runs", phaseOneFirstFile, "exit 11", nil, 1, "terraform test exited 11"},
		{"zero exit with short count", phaseOneFirstFile, "exit 0", nil, 1, "only 1 of 2 baseline run block(s) executed"},
		{"filtered nonzero exit", "", "exit 1", []string{phaseOneFirstFile}, 1, "terraform test exited 1"},
		{"zero executed runs", "tests/missing.tftest.hcl", "exit 0", nil, 0, "no run block executed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := baseConfig(t, phaseOneModule(t, "output.value != null"))
			request.IncludeOperators = []string{string(mutation.BoolLiteralFlip)}
			request.TestSelection = test.selection
			request.TerraformBinary = phaseOneTerraform(t, test.filter, test.completion)

			for range 2 {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}

				if len(result.Mutants) != 1 || len(result.Errors) != 1 {
					t.Fatalf("want one mutant and one execution error: %+v", result)
				}

				mutant := result.Mutants[0]
				failure := result.Errors[0]
				if failure.MutantID != mutant.ID || failure.Site != mutant.Site ||
					!strings.Contains(failure.Message, test.message) {
					t.Fatalf("execution error = %+v, want %q for %s", failure, test.message, mutant.ID)
				}

				if mutant.State != report.Pending || mutant.Verdict != nil || mutant.ExecutedRuns != test.wantRuns {
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
		})
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
		{"full passing suite", "output.value != null", "", "exit 0", 2, report.Survived, false},
		{"failing short suite", "output.value", phaseOneFirstFile, "exit 1", 1, report.Killed, false},
		{"erroring short suite", `output.value ? true : tonumber("invalid") == 0`, phaseOneFirstFile, "exit 1", 1, report.KilledByError, true},
		{"timed out short suite", "output.value != null", phaseOneFirstFile, "sleep 300", 1, report.Timeout, false},
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

func TestPhaseOneFilteredPassingRunsReachPhaseTwo(t *testing.T) {
	t.Parallel()

	request := baseConfig(t, phaseOneModule(t, "output.value != null"))
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
		if run.Phase == 2 {
			return
		}
	}

	t.Fatalf("a filtered short stream was refused before fingerprinting: %+v", result.Errors)
}
