package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestRunReportsKilledAndSurvivedOutputs(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	before := treeDigest(t, module)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := stateOf(t, result, "output.asserted"); got != report.Killed {
		t.Fatalf("output.asserted state = %s, want %s", got, report.Killed)
	}

	if got := stateOf(t, result, "output.unasserted"); got != report.Survived {
		t.Fatalf("output.unasserted state = %s, want %s", got, report.Survived)
	}

	if result.Count(report.Killed) == 0 || result.Count(report.Survived) == 0 {
		t.Fatalf("expected both kills and survivors, got %v", result.Metrics.Counts)
	}

	assertTreeUnchanged(t, module, before)
}

func TestSurvivorsExitNonZeroAndCleanRunsExitZero(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitFindings { //nolint:exhaustruct // no gate requested.
		t.Fatalf("exit code with survivors = %d, want %d", code, report.ExitFindings)
	}

	clean := copyFixture(t, "all-killed")

	cleanResult, err := engine.Run(t.Context(), baseConfig(t, clean))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if cleanResult.Count(report.Survived) != 0 {
		t.Fatalf("all-killed should have no survivors, got %v", cleanResult.Metrics.Counts)
	}

	if code := cleanResult.ExitCode(report.Gate{}); code != report.ExitClean { //nolint:exhaustruct // no gate requested.
		t.Fatalf("exit code without survivors = %d, want %d", code, report.ExitClean)
	}
}

func TestEveryMutantCarriesASingleLineDiff(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.Diff == "" {
			t.Fatalf("mutant %s has no diff", mutant.ID)
		}

		removed := strings.Count(mutant.Diff, "\n-") + boolToInt(strings.HasPrefix(mutant.Diff, "-"))
		if removed > 2 {
			t.Fatalf("mutant %s diff is not minimal:\n%s", mutant.ID, mutant.Diff)
		}

		if mutant.Range.Start.Line == 0 {
			t.Fatalf("mutant %s has no source location", mutant.ID)
		}
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

func TestRedBaselineAbortsNamingTheFailingRun(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "red-baseline")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrBaselineRed) {
		t.Fatalf("error = %v, want a red baseline refusal", err)
	}

	if !strings.Contains(err.Error(), "wrong_expectation") {
		t.Fatalf("refusal does not name the failing run block: %v", err)
	}
}

func TestBaselineWithoutRunBlocksAbortsLoudly(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "empty-tests")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrBaselineNoRuns) {
		t.Fatalf("error = %v, want a zero-run refusal", err)
	}
}

func TestTerraformBelowTheTestFrameworkIsRefused(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	// A stub of the binary, not of the engine's Terraform integration: the
	// refusal has to be provable without installing an obsolete release.
	config.TerraformBinary = stubTerraform(t, "1.5.7")

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrTerraformVersion) {
		t.Fatalf("error = %v, want a version refusal", err)
	}

	if !strings.Contains(err.Error(), "1.6") {
		t.Fatalf("refusal does not explain the minimum version: %v", err)
	}
}

func TestNoCoverageIsAssignedWithoutExecuting(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "nocoverage")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	rootMutant := stateOf(t, result, "output.root_only")
	if rootMutant != report.NoCoverage {
		t.Fatalf("root output state = %s, want %s", rootMutant, report.NoCoverage)
	}

	for _, mutant := range result.Mutants {
		if mutant.State == report.NoCoverage && len(mutant.Runs) > 0 {
			t.Fatalf("NoCoverage mutant %s executed %d runs", mutant.ID, len(mutant.Runs))
		}
	}

	if stateOf(t, result, "output.echo") == report.NoCoverage {
		t.Fatal("the module the run block targets must be covered")
	}
}

func TestUpwardModuleSourceIsMutatedAndTested(t *testing.T) {
	t.Parallel()

	root := copyFixture(t, "upward")
	before := treeDigest(t, root)

	config := baseConfig(t, root+"/root")

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := stateOf(t, result, "output.name"); got != report.Killed {
		t.Fatalf("shared module output state = %s, want %s", got, report.Killed)
	}

	found := false

	for _, mutant := range result.Mutants {
		if strings.HasPrefix(mutant.Range.File, "shared/") {
			found = true
		}
	}

	if !found {
		t.Fatalf("no mutant was generated in the upward module; sites are %v", sites(result))
	}

	assertTreeUnchanged(t, root, before)
}

func TestUnformattedSourceIsWarnedAbout(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unformatted")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Fatal("expected a formatting warning, got none")
	}

	if !strings.Contains(strings.Join(result.Warnings, " "), "formatted") {
		t.Fatalf("warnings do not mention formatting: %v", result.Warnings)
	}
}

func TestMissingLockFileStillExecutesEveryMutant(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "mocked-null")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Count(report.Invalid) > 0 {
		t.Fatalf("a missing lock file classified %d mutants Invalid", result.Count(report.Invalid))
	}

	if result.Count(report.Killed) == 0 {
		t.Fatalf("expected kills against the mocked provider, got %v", result.Metrics.Counts)
	}
}

func TestPreviewExecutesNothing(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	before := treeDigest(t, module)

	config := baseConfig(t, module)
	config.Preview = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("preview generated no mutants")
	}

	for _, mutant := range result.Mutants {
		if mutant.State != report.Pending {
			t.Fatalf("preview mutant %s has state %s", mutant.ID, mutant.State)
		}

		if len(mutant.Runs) != 0 {
			t.Fatalf("preview mutant %s recorded runs", mutant.ID)
		}

		if mutant.Diff == "" {
			t.Fatalf("preview mutant %s has no diff", mutant.ID)
		}
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitClean { //nolint:exhaustruct // no gate requested.
		t.Fatalf("preview exit code = %d, want %d", code, report.ExitClean)
	}

	assertTreeUnchanged(t, module, before)
}

func TestTimeoutLandsInTheDenominatorAndFailsTheGate(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	// A budget no real Terraform invocation can meet, which is the only way to
	// observe the timeout path without waiting for the production floor.
	config.TimeoutFactor = 0.0001
	config.TimeoutFloor = time.Millisecond

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Count(report.Timeout) == 0 {
		t.Fatalf("expected timeouts, got %v", result.Metrics.Counts)
	}

	if !result.Metrics.Incomplete {
		t.Fatal("timeouts must mark the score incomplete")
	}

	if result.Metrics.Scored < result.Count(report.Timeout) {
		t.Fatal("timeouts must sit in the denominator")
	}

	gate := report.Gate{MinScore: 0, HasMinScore: true, AllowIncompleteScore: false}
	if code := result.ExitCode(gate); code != report.ExitFindings {
		t.Fatalf("incomplete score exit code = %d, want %d", code, report.ExitFindings)
	}

	permissive := report.Gate{MinScore: 0, HasMinScore: true, AllowIncompleteScore: true}
	if code := result.ExitCode(permissive); code != report.ExitClean {
		t.Fatalf("--allow-incomplete-score exit code = %d, want %d", code, report.ExitClean)
	}
}

func TestSelectionMatchingNothingIsAnOperationalFailure(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	config := baseConfig(t, module)
	config.TestSelection = []string{"tests/does-not-exist.tftest.hcl"}

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Errors) == 0 {
		t.Fatalf("a selection matching nothing must be an operational failure, got %v", result.Metrics.Counts)
	}

	if result.Count(report.Survived) > 0 {
		t.Fatal("a selection matching nothing must never report survivors")
	}

	if code := result.ExitCode(report.Gate{}); code != report.ExitOperational { //nolint:exhaustruct // no gate requested.
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}
}

func TestReportIsIdenticalAtEveryParallelismLevel(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	sequential := runWithJobs(t, module, 1)
	concurrent := runWithJobs(t, module, oversubscribedJobs)

	if len(sequential.Mutants) != len(concurrent.Mutants) {
		t.Fatalf("mutant counts differ: %d vs %d", len(sequential.Mutants), len(concurrent.Mutants))
	}

	for index, mutant := range sequential.Mutants {
		other := concurrent.Mutants[index]

		if mutant.ID != other.ID || mutant.State != other.State {
			t.Fatalf("mutant %d differs: %s/%s vs %s/%s", index,
				mutant.ID, mutant.State, other.ID, other.State)
		}
	}

	if sequential.Metrics.MutationScore != concurrent.Metrics.MutationScore {
		t.Fatalf("scores differ: %v vs %v", sequential.Metrics, concurrent.Metrics)
	}
}

func runWithJobs(t *testing.T, module string, jobs int) report.Report {
	t.Helper()

	config := baseConfig(t, module)
	config.Jobs = jobs

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run with %d jobs: %v", jobs, err)
	}

	return result
}

func TestCancellationLeavesTheSourceTreeIntact(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	before := treeDigest(t, module)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _ = engine.Run(ctx, baseConfig(t, module))

	assertTreeUnchanged(t, module, before)
}
