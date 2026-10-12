package engine_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The deadlines every outbound call carries. Each test here wraps the real
// Terraform binary — or git — in a script that sleeps exactly where the
// deadline should fire, and asserts the run ends operationally, or lands the
// mutant as a Timeout, well inside the sleep: a hung dependency must end the
// run, never hang it.

// sleepingTerraform wraps the real Terraform binary and sleeps for the given
// duration instead of executing whenever the bash condition over the
// wrapper's arguments holds. The conditions key on `-chdir=<dir>` ($1) and
// the subcommand ($2), so a wrapper can fire on one pipeline phase alone.
func sleepingTerraform(t *testing.T, condition string, nap int) string {
	t.Helper()

	script := `#!/usr/bin/env bash
if ` + condition + `; then
  sleep ` + strconv.Itoa(nap) + `
fi
exec terraform "$@"
`

	path := filepath.Join(t.TempDir(), "terraform-sleeping")

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the wrapper must run.
		t.Fatalf("writing terraform wrapper: %v", err)
	}

	return path
}

// TestAnInitThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase holds
// the preflight contract: terraform init, which downloads providers over the
// network, is bounded by an absolute cap, and a dependency that never returns
// ends the run as an operational failure naming the phase it hung in — within
// the cap, not after the sleep.
func TestAnInitThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.TerraformBinary = sleepingTerraform(t, `[ "$2" = init ]`, 300)
	config.PreflightTimeout = 2 * time.Second

	started := time.Now()
	_, err := engine.Run(t.Context(), config)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("an init that never returned produced no error")
	}

	if !errors.Is(err, tfexec.ErrNotExecuted) {
		t.Fatalf("error = %v, want an operational terraform failure", err)
	}

	if !strings.Contains(err.Error(), "init") || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the phase and the cap: %v", err)
	}

	if elapsed > 30*time.Second {
		t.Fatalf("the cap was not honoured: the run took %s", elapsed)
	}
}

// TestABaselineThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase is
// the same contract for the baseline runs: they are timed for calibration and
// fingerprinted, so a baseline that never returns leaves the run nothing to
// grade and must end it operationally.
func TestABaselineThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	// The warm workspace is the baseline's sandbox; a mutant sandbox is
	// never named warm, so the wrapper fires on the baseline alone.
	config.TerraformBinary = sleepingTerraform(t, `[ "$2" = test ] && [[ "$1" == */warm ]]`, 300)
	config.PreflightTimeout = 2 * time.Second

	started := time.Now()
	_, err := engine.Run(t.Context(), config)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("a baseline that never returned produced no error")
	}

	if !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the phase and the cap: %v", err)
	}

	if elapsed > 30*time.Second {
		t.Fatalf("the cap was not honoured: the run took %s", elapsed)
	}
}

// TestAPhaseTwoRunThatOutlivesTheBudgetLandsInTheDenominator holds the
// survivor-side contract: the verbose fingerprint run is bounded by the same
// budget phase one was, so an overrun there is a Timeout verdict counted in
// the score's denominator and the score is marked incomplete — not a hang,
// and not a survival judged from a truncated run.
//
// The wrapper sleeps only on verbose runs of mutant sandboxes: phase one is
// not verbose and the baseline runs in the warm workspace, so both complete,
// and the deadline provably fires in phase two — the mutant carries the
// phase-one run block it completed.
func TestAPhaseTwoRunThatOutlivesTheBudgetLandsInTheDenominator(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.TerraformBinary = sleepingTerraform(t,
		`[ "$2" = test ] && [[ "$1" == */m0* ]] && [[ "$*" == *-verbose* ]]`, 30)
	config.TimeoutFloor = 2 * time.Second
	config.IncludeOperators = []string{string(mutation.StrCase)}

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Count(report.Timeout) == 0 {
		t.Fatalf("no mutant landed as a Timeout: %v", result.Metrics.Counts)
	}

	completedPhaseOne := false

	for _, mutant := range result.Mutants {
		if mutant.State != report.Timeout {
			continue
		}

		for _, run := range mutant.Runs {
			if run.Phase == 1 && run.Status != tfexec.StatusSkip {
				completedPhaseOne = true
			}
		}
	}

	if !completedPhaseOne {
		t.Fatal("no Timeout mutant carries a completed phase-one run, so the deadline did not provably fire in phase two")
	}

	if !result.Metrics.Incomplete {
		t.Fatal("a run with a Timeout was not marked incomplete")
	}
}

// TestACharacterisationStagedRunThatOutlivesTheBudgetFailsTheRun is the same
// contract for the generation direction: the staged runs a characterisation
// harvests and verifies with are bounded too, so a suite that never returns
// ends the run operationally instead of pinning whatever a truncated harvest
// happened to contain.
func TestACharacterisationStagedRunThatOutlivesTheBudgetFailsTheRun(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "untested-zero-output")

	config := characteriseRequest(t, module)
	config.TerraformBinary = sleepingTerraform(t, `[[ "$1" == *harvest-1* ]]`, 30)
	// The staged runs are infrastructure: they carry the absolute preflight
	// cap, not the mutant budget, so the test shortens that cap.
	config.PreflightTimeout = 2 * time.Second

	_, err := engine.Run(t.Context(), &config)
	if err == nil {
		t.Fatal("a staged run that never returned produced no error")
	}

	if !strings.Contains(err.Error(), "harvest") || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the phase and the budget: %v", err)
	}
}

// TestAGitCallThatOutlivesItsCapFailsTheRun bounds the git subprocesses
// behind --since: a short fixed cap, and a run that ends operationally when
// it fires, because a hung git hangs the whole selection otherwise.
func TestAGitCallThatOutlivesItsCapFailsTheRun(t *testing.T) {
	module := gitFixture(t, "skeleton")
	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)

	realGit, lookupErr := exec.LookPath("git")
	if lookupErr != nil {
		t.Fatalf("locating git: %v", lookupErr)
	}

	shimDir := t.TempDir()
	// gitRun invokes git as `git -C <dir> <subcommand> ...`, so the subcommand
	// is the shim's third argument.
	script := `#!/usr/bin/env bash
if [ "$3" = diff ]; then sleep 60; fi
exec ` + realGit + ` "$@"
`

	writeErr := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o700) //nolint:gosec // the shim must run.
	if writeErr != nil {
		t.Fatalf("writing git shim: %v", writeErr)
	}

	engine.SetGitTimeout(t, 500*time.Millisecond)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	config := baseConfig(t, module)
	config.Since = sinceHead

	started := time.Now()
	_, runErr := engine.Run(t.Context(), config)
	elapsed := time.Since(started)

	if runErr == nil {
		t.Fatal("a git call that never returned produced no error")
	}

	if !strings.Contains(runErr.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the cap: %v", runErr)
	}

	if elapsed > 30*time.Second {
		t.Fatalf("the cap was not honoured: the run took %s", elapsed)
	}
}
