package engine_test

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/andrewesweet/tf-mut/internal/testsupport"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

const preflightFixture = "skeleton"

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

	testsupport.WriteExecutable(t, path, script)

	return path
}

func TestAFmtDeadlineFailsEveryWarmUpCommand(t *testing.T) {
	t.Parallel()

	for _, command := range []string{runCommand, previewCommand, suggestCommand, curateCommand, characteriseCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			fixture := preflightFixture
			if command == characteriseCommand {
				fixture = "untested-zero-output"
			}

			common := commonRequest(t, copyFixture(t, fixture))
			common.TerraformBinary = sleepingTerraform(t, `[ "$2" = fmt ]`, 300)
			common.PreflightTimeout = 2 * time.Second

			requests := map[string]engine.Request{
				runCommand:          engine.RunRequest{Common: common},
				previewCommand:      engine.PreviewRequest{Common: common},
				suggestCommand:      engine.SuggestRequest{Common: common},
				curateCommand:       engine.CurateRequest{Common: common},
				characteriseCommand: engine.CharacteriseRequest{Common: common},
			}

			started := time.Now()
			_, err := engine.Run(t.Context(), requests[command])
			if !errors.Is(err, tfexec.ErrNotExecuted) {
				t.Fatalf("error = %v, want an operational terraform failure", err)
			}

			if !strings.Contains(err.Error(), "fmt") || !strings.Contains(err.Error(), "did not finish within") {
				t.Fatalf("the failure does not name fmt and its cap: %v", err)
			}

			if elapsed := time.Since(started); elapsed > 30*time.Second {
				t.Fatalf("the cap was not honoured: the run took %s", elapsed)
			}

			entries, readErr := os.ReadDir(common.WorkDir)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("work directory was not cleaned: %v, %v", entries, readErr)
			}
		})
	}
}

func TestCancellationDuringFmtFailsPreview(t *testing.T) {
	t.Parallel()

	request := previewRequest(t, copyFixture(t, preflightFixture))
	request.TerraformBinary = sleepingTerraform(t, `[ "$2" = fmt ] && touch "$0.started"`, 300)
	request.PreflightTimeout = time.Minute

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	finished := make(chan error, 1)
	go func() {
		_, err := engine.Run(ctx, request)
		finished <- err
	}()

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	waiting := true
	for waiting {
		select {
		case err := <-finished:
			t.Fatalf("preview ended before fmt started: %v", err)
		case <-deadline.C:
			t.Fatal("preview never reached fmt")
		case <-ticker.C:
			if _, err := os.Stat(request.TerraformBinary + ".started"); err == nil {
				waiting = false
			}
		}
	}

	cancel()

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, tfexec.ErrNotExecuted) {
			t.Fatalf("error = %v, want an operational cancellation", err)
		}
	case <-deadline.C:
		t.Fatal("preview did not stop after cancellation during fmt")
	}

	entries, err := os.ReadDir(request.WorkDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("work directory was not cleaned: %v, %v", entries, err)
	}
}

// TestAnInitThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase holds
// the preflight contract: terraform init, which downloads providers over the
// network, is bounded by an absolute cap, and a dependency that never returns
// ends the run as an operational failure naming the phase it hung in — within
// the cap, not after the sleep.
func TestAnInitThatOutlivesItsPreflightCapFailsOperationallyNamingThePhase(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, preflightFixture)

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

	module := copyFixture(t, preflightFixture)

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

	if !errors.Is(err, tfexec.ErrNotExecuted) {
		t.Fatalf("error = %v, want an operational terraform failure", err)
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

	module := copyFixture(t, preflightFixture)

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

	if !errors.Is(err, tfexec.ErrNotExecuted) {
		t.Fatalf("error = %v, want an operational terraform failure", err)
	}

	if !strings.Contains(err.Error(), "harvest") || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the phase and the budget: %v", err)
	}
}

func TestAScaffoldAnswerVerificationDeadlineFailsBeforeWriting(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	removeTests(t, module)
	before := treeDigest(t, module)

	request := characteriseRequest(t, module)
	request.UntilDry = true
	opened, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("opening scaffolds: %v", err)
	}

	identifier := scaffoldFor(t, opened.Characterisation, "var.size.validation")
	request.Answers = []string{identifier + "={ size = 0 }"}
	request.Write = true
	request.PreflightTimeout = 2 * time.Second
	request.TerraformBinary = sleepingTerraform(t,
		`[ "$2" = test ] && [[ "$1" == */scaffold-*/verify ]] && touch "$0.started"`, 300)

	started := time.Now()
	_, err = engine.Run(t.Context(), request)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, tfexec.ErrNotExecuted) {
		t.Fatalf("error = %v, want an operational staged-verification deadline", err)
	}

	if _, markerErr := os.Stat(request.TerraformBinary + ".started"); markerErr != nil {
		t.Fatalf("the answered scaffold never reached verification: %v", markerErr)
	}

	if !strings.Contains(err.Error(), "staged run verify") || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("the failure does not name staged verification and its cap: %v", err)
	}

	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("the cap was not honoured: the run took %s", elapsed)
	}

	if err := t.Context().Err(); err != nil {
		t.Fatalf("the root context was cancelled: %v", err)
	}

	assertTreeUnchanged(t, module, before)
	entries, readErr := os.ReadDir(request.WorkDir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("work directory was not cleaned: %v, %v", entries, readErr)
	}
}

// TestAGitCallThatOutlivesItsCapFailsTheRun bounds the git subprocesses
// behind --since: a short fixed cap, and a run that ends operationally when
// it fires, because a hung git hangs the whole selection otherwise.
func TestAGitCallThatOutlivesItsCapFailsTheRun(t *testing.T) {
	module := gitFixture(t, preflightFixture)
	testsupport.WriteFile(t, filepath.Join(module, "extra.tf"), extraResource)

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

	testsupport.WriteExecutable(t, filepath.Join(shimDir, "git"), script)

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

	if !errors.Is(runErr, engine.ErrGitDeadline) {
		t.Fatalf("error = %v, want a git deadline failure", runErr)
	}

	if !strings.Contains(runErr.Error(), "did not finish within") {
		t.Fatalf("the failure does not name the cap: %v", runErr)
	}

	if elapsed > 30*time.Second {
		t.Fatalf("the cap was not honoured: the run took %s", elapsed)
	}
}

type gitLookupFallbackCase struct {
	name      string
	condition string
	todos     bool
	pack      bool
}

//nolint:paralleltest // Git shim changes the process PATH and package-global Git deadline.
func TestGitDeadlinesAreFatalThroughLookupFallbacks(t *testing.T) {
	cases := []gitLookupFallbackCase{
		{name: "checkout-preview", condition: `[ "$4" = --show-toplevel ]`},
		{name: "checkout-todos", condition: `[ "$4" = --show-toplevel ]`, todos: true},
		{name: "pack-repository", condition: `[[ "$2" == */packs/ ]] && [ "$4" = --is-inside-work-tree ]`, pack: true},
		{
			name:      "pack-committed",
			condition: `[[ "$2" == */packs/ ]] && [ "$3" = diff ] && [[ "$6" == *...HEAD ]]`,
			pack:      true,
		},
		{name: "pack-working-tree", condition: `[[ "$2" == */packs/ ]] && [ "$3" = diff ] && [ "$6" = HEAD ]`, pack: true},
		{name: "pack-untracked", condition: `[[ "$2" == */packs/ ]] && [ "$3" = ls-files ]`, pack: true},
	}
	for _, testCase := range cases {
		for _, deadline := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/deadline=%t", testCase.name, deadline), func(t *testing.T) {
				runGitLookupFallbackCase(t, testCase, deadline)
			})
		}
	}
}

func runGitLookupFallbackCase(t *testing.T, testCase gitLookupFallbackCase, deadline bool) {
	t.Helper()

	fixture := preflightFixture
	if testCase.pack {
		fixture = packsFixture
	}
	module := gitFixture(t, fixture)
	request := previewRequest(t, module)
	if testCase.pack {
		request.Packs = []string{acmePack}
		request.Since = sinceHead
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	action := "exit 1"
	if deadline {
		action = "sleep 60"
	}
	shim := filepath.Join(shimDir, "git")
	testsupport.WriteExecutable(t, shim, "#!/usr/bin/env bash\nif "+testCase.condition+"; then "+action+
		"; fi\nexec \"$TF_MUT_REAL_GIT\" \"$@\"\n")
	t.Setenv("TF_MUT_REAL_GIT", realGit)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	engine.SetGitTimeout(t, 500*time.Millisecond)

	var command engine.Request = request
	if testCase.todos {
		command = engine.TodosRequest{Common: request.Common}
	}
	result, err := engine.Run(t.Context(), command)
	switch {
	case deadline:
		if err == nil || !errors.Is(err, engine.ErrGitDeadline) {
			t.Fatalf("error = %v, want a git deadline failure", err)
		}

		// The deadline's wording is published stderr text; the sentinel
		// identifies which failure happened.
		if !strings.Contains(err.Error(), "did not finish within") {
			t.Fatalf("the failure does not name the cap: %v", err)
		}
	case err != nil:
		t.Fatalf("an ordinary lookup failure stopped the command: %v", err)
	default:
		if testCase.pack && result.Selection.ForcedFull == "" {
			t.Fatal("an ordinary pack lookup failure did not force the full population")
		}
	}
}
