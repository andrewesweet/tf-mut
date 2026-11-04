package engine_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/store"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

const (
	// failfastWatchedSite is the mutated site the first run's assertion
	// observes; failfastIgnoredSite is the one nothing observes.
	failfastWatchedSite = "terraform_data.watched.input"
	failfastIgnoredSite = "terraform_data.ignored.input"

	// failfastParallelSite is the site the parallel fixture's assertions all
	// observe, failfastParallelRuns the run blocks that execute it
	// concurrently, and failfastParallelAssertions the assertions each block
	// makes.
	failfastParallelSite       = "terraform_data.watched.input"
	failfastParallelRuns       = 12
	failfastParallelAssertions = 16
	failfastParallelLaterRun   = "after"
	assertionFailureSummary    = "Test assertion failed"
)

// truncationDocument is the slice of the published JSON document the wire
// assertions read: named types, because the assertion is about the shape the
// schema publishes, not the Go struct.
type truncationDocument struct {
	Mutants []truncationMutant `json:"mutants"`
}

type truncationMutant struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	RunsTruncated *bool  `json:"runs_truncated"`
}

// The stream's status vocabulary as this file reads it, so a mis-spelled
// expectation is a compile error and the literals stay single-sourced.
const (
	runFail        = "fail"
	runError       = "error"
	runSkip        = "skip"
	runOS          = "linux"
	completeRecord = "complete"
)

func runFailFastFixture(t *testing.T, module string, operator mutation.Operator) report.Report {
	t.Helper()

	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(operator)}

	result, err := engine.Run(t.Context(), &config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	return result
}

func TestAFailFastKillKeepsItsFilesRunsAndVerdict(t *testing.T) {
	t.Parallel()

	result := runFailFastFixture(t, copyFixture(t, "failfast"), mutation.StrCase)

	killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
	if killed.State != report.Killed {
		t.Fatalf("watched mutant state = %s, want %s", killed.State, report.Killed)
	}

	if killed.RunsTruncated {
		t.Fatal("a completed sole file must not name a truncation")
	}

	if killed.ExecutedRuns != 3 {
		t.Fatalf("executed runs = %d, want all three runs in the failing file", killed.ExecutedRuns)
	}

	if len(killed.Runs) != 3 || killed.Runs[0].Status != runFail {
		t.Fatalf("runs = %v, want all three failing-file records", killed.Runs)
	}

	if killed.DeadlineReached {
		t.Fatal("the stop must be the fail-fast decision, not the budget")
	}

	// A survivor runs every block: its record stays complete, because nothing
	// failed and nothing was stopped.
	survivor := mutantAt(t, result, string(mutation.StrCase), failfastIgnoredSite)
	if survivor.State != report.Survived {
		t.Fatalf("ignored mutant state = %s, want %s", survivor.State, report.Survived)
	}

	if survivor.RunsTruncated {
		t.Fatal("a mutant that ran every block must not name a truncation")
	}

	if survivor.ExecutedRuns != 3 || len(survivor.Runs) != 3 {
		t.Fatalf("survivor executed %d of %d run records, want all three", survivor.ExecutedRuns, len(survivor.Runs))
	}

	// The wire shape carries the flag on both mutants: present everywhere,
	// true only where the record was cut short.
	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	var document truncationDocument
	if err := json.Unmarshal([]byte(encoded.String()), &document); err != nil {
		t.Fatalf("decoding the report document: %v", err)
	}

	for _, mutant := range document.Mutants {
		if mutant.RunsTruncated == nil {
			t.Fatalf("mutant %s (state %s) carries no runs_truncated flag", mutant.ID, mutant.State)
		}
	}
}

// TestParallelRunsKeepTheArmingRunsAssertionDiagnostics holds the parallel-run
// half of the fail-fast contract: the assertion diagnostics a stop must keep
// belong to the arming run's file, so another run's start, completion or plan
// event interleaved before them is not the boundary. The verdict was never in
// doubt — the first failing run decides Killed — and this test holds the
// evidence the stop promises to retain after such an interleaving. The fixture's
// twelve blocks exceed the ten Terraform starts at once, so later start events
// can land mid-stream as well.
func TestParallelRunsKeepTheArmingRunsAssertionDiagnostics(t *testing.T) {
	t.Parallel()

	result := runFailFastFixture(t, copyFixture(t, "failfast-parallel"), mutation.StrCase)

	killed := mutantAt(t, result, string(mutation.StrCase), failfastParallelSite)
	if killed.State != report.Killed {
		t.Fatalf("parallel mutant state = %s, want %s", killed.State, report.Killed)
	}

	// The arming run is the first block the record completed fail: runs are
	// recorded in completion order, and its completion is what armed the stop.
	arming := ""
	for _, run := range killed.Runs {
		if run.Status == runFail {
			arming = run.Run

			break
		}
	}

	if arming == "" {
		t.Fatalf("runs = %v, want the failing completion that armed the stop", killed.Runs)
	}

	retained := 0
	for _, diagnostic := range killed.Diagnostics {
		if diagnostic.TestRun == arming && diagnostic.Summary == assertionFailureSummary {
			retained++
		}
	}

	if retained != failfastParallelAssertions {
		t.Fatalf("arm %q kept %d of its %d assertion diagnostics (%d in the whole record), want all of them: "+
			"the parallel interleaving discarded the evidence that explains the kill",
			arming, retained, failfastParallelAssertions, len(killed.Diagnostics))
	}

	// The stop is still taken: the file after the parallel one never runs,
	// and the record names that truncation beside the verdict.
	if !killed.RunsTruncated {
		t.Fatal("the later file must not have run: the stop ended the invocation")
	}

	if killed.ExecutedRuns != failfastParallelRuns || len(killed.Runs) != failfastParallelRuns {
		t.Fatalf("executed runs = %d of %d run records, want the parallel file's %d blocks alone",
			killed.ExecutedRuns, len(killed.Runs), failfastParallelRuns)
	}

	for _, run := range killed.Runs {
		if run.Run == failfastParallelLaterRun {
			t.Fatalf("run %q from the later file was recorded, want the stop to have ended the invocation", run.Run)
		}
	}

	if killed.DeadlineReached {
		t.Fatal("the stop must be the fail-fast decision, not the budget")
	}
}

func TestAnErrorRunIsNeverStoppedEarly(t *testing.T) {
	t.Parallel()

	result := runFailFastFixture(t, copyFixture(t, "failfast-error"), mutation.VarDefaultChange)

	errored := mutantAt(t, result, string(mutation.VarDefaultChange), "var.token.default")
	if errored.State != report.KilledByError {
		t.Fatalf("token mutant state = %s, want %s", errored.State, report.KilledByError)
	}

	// The error did not stop the invocation: the run block after the error is
	// recorded with the skip Terraform reports for it, which is exactly the
	// record a stop-on-error would lose.
	if errored.RunsTruncated {
		t.Fatal("an error run must not truncate the record")
	}

	if len(errored.Runs) != 2 {
		t.Fatalf("runs = %v, want the error and the skip that followed it", errored.Runs)
	}

	if errored.Runs[0].Status != runError || errored.Runs[1].Status != runSkip {
		t.Fatalf("run statuses = %s, %s; want error then skip", errored.Runs[0].Status, errored.Runs[1].Status)
	}
}

func TestRealInfrastructureOptInKeepsEveryRunAfterAFailure(t *testing.T) {
	t.Parallel()

	request := baseConfig(t, copyFixture(t, "failfast"))
	request.IncludeOperators = []string{string(mutation.StrCase)}
	request.AllowRealInfrastructure = true
	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
	if killed.State != report.Killed || killed.RunsTruncated || killed.ExecutedRuns != 3 || len(killed.Runs) != 3 {
		t.Fatalf("real-infrastructure opt-in lost complete kill evidence: %+v", killed)
	}
	for _, run := range killed.Runs {
		if run.Status != runFail {
			t.Fatalf("run %s = %s, want fail", run.Run, run.Status)
		}
	}
}

func completeFailFastRequest(t *testing.T, runs int, filtered, first bool) engine.RunRequest {
	t.Helper()

	module := copyFixture(t, "failfast")
	var suite strings.Builder
	for index := range runs {
		condition := "output.watched != null"
		if index == runs-1 || first && index == 0 {
			condition = `output.watched == "kept"`
		}
		fmt.Fprintf(&suite, `run "check_%d" {
  command = plan
  assert {
    condition = %s
    error_message = "watched value must satisfy the condition"
  }
}
`, index, condition)
	}
	testsupport.WriteFile(t, filepath.Join(module, unitTestFile), suite.String())
	request := baseConfig(t, module)
	request.IncludeOperators = []string{string(mutation.StrCase)}
	if filtered {
		testsupport.WriteFile(t, filepath.Join(module, "tests/other.tftest.hcl"), `run "other" {
  command = plan
}
`)
		request.TestSelection = []string{unitTestFile}
	}
	return request
}

func TestCompleteFailFastRecordsStayCompleteThroughTheCache(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		runs     int
		filtered bool
		first    bool
	}{
		{"sole failure", 1, false, false},
		{"final failure", 3, false, false},
		{"early failure", 3, false, true},
		{"filtered sole failure", 1, true, false},
		{"filtered final failure", 3, true, false},
		{"filtered early failure", 3, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := completeFailFastRequest(t, test.runs, test.filtered, test.first)

			for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
				if killed.State != report.Killed || killed.RunsTruncated ||
					killed.ExecutedRuns != test.runs || len(killed.Runs) != test.runs {
					t.Fatalf("complete failing record = %+v", killed)
				}
				if killed.Provenance == nil || killed.Provenance.Execution != execution {
					t.Fatalf("provenance = %+v, want %s", killed.Provenance, execution)
				}
			}
		})
	}
}

func TestPreFileBoundaryFailFastObservationsAreNotReplayed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		fixture     string
		runs        int
		diagnostics int
		truncated   bool
	}{
		{"failfast", 3, 3, false},
		{"failfast-parallel", failfastParallelRuns, failfastParallelRuns * failfastParallelAssertions, true},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			t.Parallel()

			request := baseConfig(t, copyFixture(t, test.fixture))
			request.IncludeOperators = []string{string(mutation.StrCase)}
			first := cachedRun(t, request)
			killed := mutantAt(t, first, string(mutation.StrCase), failfastWatchedSite)
			rewindFailFastObservation(t, request.ModuleDir, killed)

			for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
				result := cachedRun(t, request)
				mutant := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
				if mutant.Provenance == nil || mutant.Provenance.Execution != execution {
					t.Fatalf("provenance = %+v, want %s", mutant.Provenance, execution)
				}
				if mutant.State != report.Killed || mutant.ExecutedRuns != test.runs || len(mutant.Runs) != test.runs ||
					len(mutant.Diagnostics) != test.diagnostics || mutant.RunsTruncated != test.truncated {
					t.Fatalf("lost current fail-fast evidence after upgrade: %+v", mutant)
				}
			}
		})
	}
}

func rewindFailFastObservation(t *testing.T, module string, mutant report.Mutant) {
	t.Helper()

	if mutant.State != report.Killed || len(mutant.Runs) == 0 || len(mutant.Diagnostics) == 0 {
		t.Fatalf("no failing observation to rewind: %+v", mutant)
	}
	paths, err := filepath.Glob(filepath.Join(cacheDir(module), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var entry store.CacheEntry
		if err := json.Unmarshal([]byte(readFile(t, path)), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.MutantID != mutant.ID {
			continue
		}

		entry.FormatVersion = "tf-mut-cache-15"
		entry.Observed, err = json.Marshal(map[string]any{
			"runs": mutant.Runs[:1], "executed_runs": 1, "runs_truncated": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		testsupport.WriteFile(t, path, string(encoded))

		return
	}
	t.Fatal("no cached kill to rewind")
}

func mixedStatusFailFastRequest(t *testing.T, filtered, truncated bool) engine.RunRequest {
	t.Helper()

	module := copyFixture(t, "failfast")
	testsupport.WriteFile(t, filepath.Join(module, "tests/error.tftest.hcl"), `run "error" {
  command = plan
  assert {
    condition     = ({ kept = true })[output.watched]
    error_message = "the watched key must exist"
  }
}

run "skipped" {
  command = plan
}
`)
	testsupport.WriteFile(t, filepath.Join(module, unitTestFile), `run "failure" {
  command = plan
  assert {
    condition     = output.watched == "kept"
    error_message = "the watched output must keep its value"
  }
}
`)
	request := baseConfig(t, module)
	request.IncludeOperators = []string{string(mutation.StrCase)}
	if filtered {
		testsupport.WriteFile(t, filepath.Join(module, "tests/omitted.tftest.hcl"), `run "omitted" {
  command = plan
}
`)
		request.TestSelection = []string{"tests/error.tftest.hcl", unitTestFile}
	}
	if truncated {
		testsupport.WriteFile(t, filepath.Join(module, "tests/z-after.tftest.hcl"), `run "after" {
  command = plan
}
`)
		if filtered {
			request.TestSelection = append(request.TestSelection, "tests/z-after.tftest.hcl")
		}
	}
	return request
}

func assertMixedRunStatuses(t *testing.T, runs []report.RunOutcome) {
	t.Helper()

	for index, status := range []string{runError, runSkip, runFail} {
		if runs[index].Status != status {
			t.Fatalf("run %d status = %s, want %s", index, runs[index].Status, status)
		}
	}
}

func TestMixedStatusRecordsKeepTheirTruncationThroughTheCache(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		filtered  bool
		truncated bool
	}{
		{completeRecord, false, false},
		{"filtered complete", true, false},
		{"prefix", false, true},
		{"filtered prefix", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := mixedStatusFailFastRequest(t, test.filtered, test.truncated)

			for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
				if killed.State != report.Killed || killed.RunsTruncated != test.truncated ||
					killed.ExecutedRuns != 2 || len(killed.Runs) != 3 {
					t.Fatalf("mixed-status record = %+v", killed)
				}
				assertMixedRunStatuses(t, killed.Runs)
				if killed.Provenance == nil || killed.Provenance.Execution != execution {
					t.Fatalf("provenance = %+v, want %s", killed.Provenance, execution)
				}
			}
		})
	}
}

func TestCurateKeepsLaterRunKillsAcrossVerdictCacheUse(t *testing.T) {
	t.Parallel()

	request := baseConfig(t, copyFixture(t, "failfast"))
	testsupport.WriteFile(t, filepath.Join(request.ModuleDir, "tests/z-after.tftest.hcl"), `run "after" {
  command = plan
  assert {
    condition     = output.watched == "kept"
    error_message = "the later file must observe the value"
  }
}
`)
	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite).RunsTruncated {
		t.Fatal("the ordinary run did not populate a truncated cached kill")
	}

	curate := engine.CurateRequest{Common: request.Common}
	for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
		result, curateErr := engine.Run(t.Context(), curate)
		if curateErr != nil {
			t.Fatalf("curate: %v", curateErr)
		}
		killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
		if killed.State != report.Killed || killed.RunsTruncated || killed.ExecutedRuns != 4 || len(killed.Diagnostics) != 4 {
			t.Fatalf("curation lost later assertion failures: %+v", killed)
		}
		if killed.Provenance == nil || killed.Provenance.Execution != execution {
			t.Fatalf("curate provenance = %+v, want %s", killed.Provenance, execution)
		}
		if len(result.Characterisation.Findings) == 0 {
			t.Fatal("identical assertions produced no redundancy findings")
		}
		for _, finding := range result.Characterisation.Findings {
			if finding.Kind == report.EmptyKillSet || !finding.PopulationAuthoritative {
				t.Fatalf("curate published false evidence: %+v", finding)
			}
		}
	}

	replayed, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	killed := mutantAt(t, replayed, string(mutation.StrCase), failfastWatchedSite)
	if !killed.RunsTruncated || killed.ExecutedRuns != 3 || killed.Provenance.Execution != report.ExecutionCached {
		t.Fatalf("verdict cache lost the original truncated record: %+v", killed)
	}
}

func TestRealInfrastructureFailureLeavesNoProviderProcessBehind(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the real-Terraform run takes seconds; -short defers it to `just test`")
	}

	requireProviderMirror(t)

	if runtime.GOOS != runOS {
		t.Skip("the leak scan reads /proc/<pid>/exe, which is Linux-only; " +
			"the group kill itself is exercised by every provider-backed test")
	}

	module := copyFixture(t, "unmocked-timeout")

	config := baseConfig(t, module)
	config.IncludeOperators = []string{string(mutation.StrCase)}
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), &config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	killed := 0
	for _, mutant := range result.Mutants {
		if mutant.State != report.Killed {
			continue
		}

		killed++
		if mutant.RunsTruncated {
			t.Fatalf("real-infrastructure mutant %s stopped early", mutant.ID)
		}

		if mutant.DeadlineReached {
			t.Fatalf("killed mutant %s reached its budget before cleanup completed", mutant.ID)
		}
	}

	if killed == 0 {
		t.Fatalf("expected at least one kill, got %v", result.Metrics.Counts)
	}

	cacheDir := pluginCacheDir(t, config.Env)

	deadline := time.Now().Add(2 * time.Second)
	for {
		leaked := executablesUnder(cacheDir)
		if len(leaked) == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("provider processes outlived Terraform cleanup: %v", leaked)
		}

		// Real elapsed time: the kernel reaps the providers asynchronously;
		// nothing signals their exit, so the poll waits.
		time.Sleep(100 * time.Millisecond)
	}
}
