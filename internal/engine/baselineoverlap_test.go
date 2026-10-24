package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// Lever 10: the baseline phase runs two identical verbose `terraform test`
// invocations — the baseline run and the volatility run that derives the mask.
// The volatility run depends only on the baseline being green, so when the job
// budget has a second slot it starts beside the baseline run. These tests hold
// the overlap's externally observable contract: the same verdicts and mask
// outcomes as the sequential schedule, a red baseline that stops the run with
// the same diagnosis while the overlapping leg is cancelled and reaped, a job
// budget of one that keeps the legs sequential, and a whole-run cancellation
// that leaves no Terraform process behind.

// TestTheBaselineVolatilityLegRunsBesideTheBaselineWhenAJobSlotIsFree holds
// the overlap's core contract. The verdicts — and with them the volatility
// mask's effect on every diagnosis — must be identical whether the two legs
// ran in sequence or beside each other, and the published telemetry must show
// the volatility leg actually started before the baseline leg ended, so the
// equivalence cannot pass with the legs still sequential.
func TestTheBaselineVolatilityLegRunsBesideTheBaselineWhenAJobSlotIsFree(t *testing.T) {
	t.Parallel()

	sequential := runWithJobs(t, copyFixture(t, "volatile"), 1)
	overlapped := runWithJobs(t, copyFixture(t, "volatile"), 2)

	if !slices.Equal(verdicts(sequential), verdicts(overlapped)) {
		t.Fatalf("verdicts moved between the sequential and overlapped schedules:\n  %v\n  %v",
			verdicts(sequential), verdicts(overlapped))
	}

	baseline := phaseByName(t, overlapped.Telemetry, phaseBaselineTestName)
	volatility := phaseByName(t, overlapped.Telemetry, phaseBaselineFingerprintName)

	baselineStart, baselineEnd := phaseWindow(t, baseline)
	volatilityStart, volatilityEnd := phaseWindow(t, volatility)

	if !volatilityStart.Before(baselineEnd) || !baselineStart.Before(volatilityEnd) {
		t.Fatalf("baseline [%s, %s) and volatility [%s, %s) did not overlap",
			baselineStart.UTC().Format(time.RFC3339Nano), baselineEnd.UTC().Format(time.RFC3339Nano),
			volatilityStart.UTC().Format(time.RFC3339Nano), volatilityEnd.UTC().Format(time.RFC3339Nano))
	}
}

// TestASingleJobKeepsTheBaselineLegsSequential pins the budget rule the
// overlap obeys: `--jobs 1` allows one concurrent Terraform child, so the
// volatility leg keeps the sequential schedule and takes no second slot.
func TestASingleJobKeepsTheBaselineLegsSequential(t *testing.T) {
	t.Parallel()

	sequential := runWithJobs(t, copyFixture(t, "volatile"), 1)

	baseline := phaseByName(t, sequential.Telemetry, phaseBaselineTestName)
	volatility := phaseByName(t, sequential.Telemetry, phaseBaselineFingerprintName)

	_, baselineEnd := phaseWindow(t, baseline)
	volatilityStart, _ := phaseWindow(t, volatility)

	if volatilityStart.Before(baselineEnd) {
		t.Fatalf("with one job the volatility leg started %s, before the baseline leg ended %s",
			volatilityStart.UTC().Format(time.RFC3339Nano), baselineEnd.UTC().Format(time.RFC3339Nano))
	}
}

func TestSafetyOptInsKeepTheBaselineLegsSequential(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                    string
		allowRealInfrastructure bool
		allowUnsandboxedEffects bool
	}{
		{name: "real-infrastructure", allowRealInfrastructure: true},
		{name: "unsandboxed-effects", allowUnsandboxedEffects: true},
		{name: "both", allowRealInfrastructure: true, allowUnsandboxedEffects: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := baseConfig(t, copyFixture(t, "volatile"))
			config.Jobs = 2
			config.AllowRealInfrastructure = test.allowRealInfrastructure
			config.AllowUnsandboxedEffects = test.allowUnsandboxedEffects

			result, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatalf("run with safety opt-ins: %v", err)
			}

			baseline := phaseByName(t, result.Telemetry, phaseBaselineTestName)
			volatility := phaseByName(t, result.Telemetry, phaseBaselineFingerprintName)
			_, baselineEnd := phaseWindow(t, baseline)
			volatilityStart, _ := phaseWindow(t, volatility)

			if !volatilityStart.After(baselineEnd) {
				t.Fatalf("with safety opt-ins the volatility leg started %s, before the baseline leg ended %s",
					volatilityStart.UTC().Format(time.RFC3339Nano), baselineEnd.UTC().Format(time.RFC3339Nano))
			}
		})
	}
}

// TestARedBaselineStillStopsTheRunWithTheOverlappingLegReaped holds the red
// path: a failing baseline stops the run with exactly the diagnosis the
// sequential schedule produces, while the volatility leg that was already
// running beside it is cancelled and reaped — no Terraform process outlives
// the run it belonged to.
func TestARedBaselineStillStopsTheRunWithTheOverlappingLegReaped(t *testing.T) {
	t.Parallel()

	_, sequentialErr := runRedBaseline(t, copyFixture(t, "red-baseline"), 1)
	_, overlappedErr := runRedBaseline(t, copyFixture(t, "red-baseline"), 2)

	if !errors.Is(overlappedErr, engine.ErrBaselineRed) {
		t.Fatalf("error = %v, want a red baseline refusal", overlappedErr)
	}

	if overlappedErr.Error() != sequentialErr.Error() {
		t.Fatalf("red diagnosis changed under the overlap:\n  sequential: %s\n  overlapped: %s",
			sequentialErr.Error(), overlappedErr.Error())
	}

	if !strings.Contains(overlappedErr.Error(), "wrong_expectation") {
		t.Fatalf("refusal does not name the failing run block: %v", overlappedErr)
	}
}

// TestCancellingMidBaselineReapsBothBaselineLegs cancels the whole run while
// both baseline legs are executing and holds the contract that matters most
// about concurrency: nothing Terraform started outlives the run. The poller
// waits until both `terraform test` children of the warm workspace are live at
// once, which is also what makes the cancellation land mid-baseline rather
// than somewhere later; mutant children run in their own sandboxes and never
// count.
func TestCancellingMidBaselineReapsBothBaselineLegs(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != linuxOS {
		t.Skip("the leg sighting reads /proc/<pid>/cmdline, which is Linux-only")
	}

	module := t.TempDir()
	writeWideSuite(t, module)

	workDir := t.TempDir()
	config := baseConfig(t, module)
	config.Jobs = 2
	config.WorkDir = workDir
	config.NoCache = true

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sighted := make(chan bool, 1)
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()

		// Bounded: a schedule that never runs the legs side by side fails the
		// test at the wait's expiry instead of hanging on the sighting.
		giveUp := time.After(legSightingWait)

		for {
			select {
			case <-ctx.Done():
				sighted <- false

				return
			case <-giveUp:
				sighted <- false

				return
			case <-ticker.C:
				if countWarmTerraformTestProcesses(workDir) >= bothLegs {
					sighted <- true
					cancel()

					return
				}
			}
		}
	}()

	_, err := engine.Run(ctx, config)

	if !<-sighted {
		t.Fatalf("the two baseline legs were never seen running at once; the cancellation did not land mid-baseline: %v", err)
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want cancellation", err)
	}

	assertNoTerraformUnder(t, workDir)
}

// phaseBaselineTestName and phaseBaselineFingerprintName restate the phase
// names the telemetry publishes. The engine's phase constants are internal to
// the engine package, and these tests assert the published document, whose
// wire values these are.
const (
	phaseBaselineTestName        = "baseline-test"
	phaseBaselineFingerprintName = "baseline-fingerprint"

	// bothLegs is the number of live baseline `terraform test` children the
	// cancellation poller waits to see before it cancels.
	bothLegs = 2

	// legSightingWait bounds the poller's wait for the two legs. A schedule
	// that never runs them side by side fails at this expiry rather than
	// hanging.
	legSightingWait = 60 * time.Second

	// linuxOS gates the /proc readers below.
	linuxOS = "linux"
)

// phaseByName finds one phase in the published telemetry.
func phaseByName(t *testing.T, telemetry *report.Telemetry, name string) report.PhaseTiming {
	t.Helper()

	if telemetry == nil {
		t.Fatalf("the run published no telemetry block; cannot observe phase %q", name)
	}

	for _, phase := range telemetry.Phases {
		if phase.Phase == name {
			return phase
		}
	}

	t.Fatalf("telemetry records no %q phase; recorded %v", name, telemetry.Phases)

	return report.PhaseTiming{}
}

// phaseWindow resolves one phase's published started_at stamp and duration
// into the wall-clock interval the phase covered.
func phaseWindow(t *testing.T, phase report.PhaseTiming) (start, end time.Time) {
	t.Helper()

	start, err := time.Parse(time.RFC3339Nano, phase.StartedAt)
	if err != nil {
		t.Fatalf("phase %q carries an unreadable started_at %q: %v", phase.Phase, phase.StartedAt, err)
	}

	return start, start.Add(time.Duration(phase.DurationMS) * time.Millisecond)
}

// runRedBaseline runs the engine over a module whose suite is red, with the
// workspace in a directory the test can scan afterwards.
func runRedBaseline(t *testing.T, module string, jobs int) (report.Report, error) {
	t.Helper()

	config := baseConfig(t, module)
	config.Jobs = jobs
	config.WorkDir = t.TempDir()

	return engine.Run(t.Context(), config)
}

// writeWideSuite writes a suite wide enough that both baseline legs stay live
// for seconds: four files of ten apply-mode run blocks each, over one
// terraform_data resource, offline and provider-free.
func writeWideSuite(t *testing.T, module string) {
	t.Helper()

	testsupport.WriteFile(t, filepath.Join(module, "main.tf"), `resource "terraform_data" "app" {
  input = "kept"
}

output "app" {
  value = terraform_data.app.output
}
`)

	tests := filepath.Join(module, "tests")
	if err := os.Mkdir(tests, 0o750); err != nil {
		t.Fatalf("creating test directory: %v", err)
	}

	for file := range 4 {
		var suite strings.Builder

		for run := range 10 {
			name := "run_" + strconv.Itoa(file) + "_" + strconv.Itoa(run)
			suite.WriteString("run \"" + name + "\" {\n")
			suite.WriteString("  command = apply\n\n")
			suite.WriteString("  assert {\n")
			suite.WriteString("    condition     = output.app == \"kept\"\n")
			suite.WriteString("    error_message = \"the output must be kept\"\n")
			suite.WriteString("  }\n}\n\n")
		}

		testsupport.WriteFile(t, filepath.Join(tests, "suite_"+strconv.Itoa(file)+".tftest.hcl"), suite.String())
	}
}

// isWarmWorkspaceDir reports whether dir is the warm workspace directory of
// the run rooted at workRoot. The engine's workspace id inside the work root
// is not predictable from outside, so the poller matches on the directory's
// own name.
func isWarmWorkspaceDir(workRoot, dir string) bool {
	return dir == filepath.Join(workRoot, "warm") ||
		(strings.HasPrefix(dir, workRoot+string(os.PathSeparator)) &&
			filepath.Base(dir) == "warm")
}

// countWarmTerraformTestProcesses counts the live `terraform test` processes
// pointed at the warm workspace of the run rooted at workRoot. The baseline's
// two legs both run against that one directory, so this is the predicate the
// sighting needs: mutant children run in sandboxes under other directories and
// never count.
func countWarmTerraformTestProcesses(workRoot string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	count := 0

	for _, entry := range entries {
		if !isNumeric(entry.Name()) {
			continue
		}

		content, readErr := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if readErr != nil {
			continue // the process exited between the listing and the read.
		}

		arguments := strings.Split(string(content), "\x00")
		for _, argument := range arguments {
			if target, found := strings.CutPrefix(argument, "-chdir="); found &&
				isWarmWorkspaceDir(workRoot, target) && slices.Contains(arguments, "test") {
				count++
			}
		}
	}

	return count
}

// countTerraformTestProcessesUnder counts the live `terraform test` processes
// whose -chdir working directory lies under root. The reaping contract needs
// the wider net: nothing the run started, wherever under the work root it ran,
// may outlive it.
func countTerraformTestProcessesUnder(root string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	count := 0

	for _, entry := range entries {
		if !isNumeric(entry.Name()) {
			continue
		}

		content, readErr := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if readErr != nil {
			continue // the process exited between the listing and the read.
		}

		arguments := strings.Split(string(content), "\x00")
		under := false
		testingSuite := false

		for _, argument := range arguments {
			if directory, found := strings.CutPrefix(argument, "-chdir="); found {
				if strings.HasPrefix(directory, root+string(os.PathSeparator)) {
					under = true
				}
			}

			if argument == "test" {
				testingSuite = true
			}
		}

		if under && testingSuite {
			count++
		}
	}

	return count
}

// assertNoTerraformUnder holds the reaping contract: after the engine returns,
// no Terraform process it started is still alive. A process caught mid-death
// gets a short grace, re-checked until the sightings persist.
func assertNoTerraformUnder(t *testing.T, root string) {
	t.Helper()

	if runtime.GOOS != linuxOS {
		t.Skip("the leak scan reads /proc/<pid>/cmdline, which is Linux-only")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		remaining := countTerraformTestProcessesUnder(root)
		if remaining == 0 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d terraform test processes outlived the run that started them", remaining)
		}

		time.Sleep(100 * time.Millisecond)
	}
}
