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
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// Fail-fast ends a mutant's terraform test child at its first failing run.
// Killed is decided from failed runs before anything else in the oracle's
// precedence, so the verdict cannot change; what changes is the published
// record: a Killed mutant's runs stop at its first failure, and the report
// names that truncation beside the verdict. These tests hold both halves —
// the unchanged verdicts and the named truncation — through the engine seam,
// against real Terraform.

const (
	// failfastWatchedSite is the mutated site the first run's assertion
	// observes; failfastIgnoredSite is the one nothing observes.
	failfastWatchedSite = "terraform_data.watched.input"
	failfastIgnoredSite = "terraform_data.ignored.input"
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
	runFail = "fail"
	runSkip = "skip"
	runOS   = "linux"
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

func TestAFailFastKillEndsAtItsFirstFailAndKeepsItsVerdict(t *testing.T) {
	t.Parallel()

	result := runFailFastFixture(t, copyFixture(t, "failfast"), mutation.StrCase)

	killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
	if killed.State != report.Killed {
		t.Fatalf("watched mutant state = %s, want %s", killed.State, report.Killed)
	}

	// The invocation ended at the first failing run: one run block remains
	// recorded, the failure itself, and the two the suite still ran before
	// this change are gone from the record.
	if !killed.RunsTruncated {
		t.Fatal("a mutant stopped at its first fail must name its truncation")
	}

	if killed.ExecutedRuns != 1 {
		t.Fatalf("executed runs = %d, want the single failing run", killed.ExecutedRuns)
	}

	if len(killed.Runs) != 1 || killed.Runs[0].Status != runFail {
		t.Fatalf("runs = %v, want the single failing run record", killed.Runs)
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

	if errored.Runs[0].Status != "error" || errored.Runs[1].Status != runSkip {
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
		{"filtered sole failure", 1, true, false},
		{"filtered final failure", 3, true, false},
		{"filtered early failure", 3, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "failfast")
			var suite strings.Builder
			for index := range test.runs {
				condition := "output.watched != null"
				if index == test.runs-1 || test.first && index == 0 {
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
			testsupport.WriteFile(t, filepath.Join(module, "tests/unit.tftest.hcl"), suite.String())
			request := baseConfig(t, module)
			request.IncludeOperators = []string{string(mutation.StrCase)}
			if test.filtered {
				testsupport.WriteFile(t, filepath.Join(module, "tests/other.tftest.hcl"), `run "other" {
  command = plan
}
`)
				request.TestSelection = []string{"tests/unit.tftest.hcl"}
			}

			wantRuns := test.runs
			if test.first {
				wantRuns = 1
			}
			for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
				if killed.State != report.Killed || killed.RunsTruncated != test.first || killed.ExecutedRuns != wantRuns || len(killed.Runs) != wantRuns {
					t.Fatalf("complete failing record = %+v", killed)
				}
				if killed.Provenance == nil || killed.Provenance.Execution != execution {
					t.Fatalf("provenance = %+v, want %s", killed.Provenance, execution)
				}
			}
		})
	}
}

func TestMixedStatusRecordsKeepTheirTruncationThroughTheCache(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		filtered  bool
		truncated bool
	}{
		{"complete", false, false},
		{"filtered complete", true, false},
		{"prefix", false, true},
		{"filtered prefix", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

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
			testsupport.WriteFile(t, filepath.Join(module, "tests/unit.tftest.hcl"), `run "failure" {
  command = plan
  assert {
    condition     = output.watched == "kept"
    error_message = "the watched output must keep its value"
  }
}
`)
			request := baseConfig(t, module)
			request.IncludeOperators = []string{string(mutation.StrCase)}
			if test.filtered {
				testsupport.WriteFile(t, filepath.Join(module, "tests/omitted.tftest.hcl"), `run "omitted" {
  command = plan
}
`)
				request.TestSelection = []string{"tests/error.tftest.hcl", "tests/unit.tftest.hcl"}
			}
			if test.truncated {
				testsupport.WriteFile(t, filepath.Join(module, "tests/z-after.tftest.hcl"), `run "after" {
  command = plan
}
`)
				if test.filtered {
					request.TestSelection = append(request.TestSelection, "tests/z-after.tftest.hcl")
				}
			}

			for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
				if killed.State != report.Killed || killed.RunsTruncated != test.truncated || killed.ExecutedRuns != 2 || len(killed.Runs) != 3 {
					t.Fatalf("mixed-status record = %+v", killed)
				}
				for index, status := range []string{"error", runSkip, runFail} {
					if killed.Runs[index].Status != status {
						t.Fatalf("run %d status = %s, want %s", index, killed.Runs[index].Status, status)
					}
				}
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
	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite).RunsTruncated {
		t.Fatal("the ordinary run did not populate a truncated cached kill")
	}

	curate := engine.CurateRequest{Common: request.Common}
	for _, execution := range []string{report.ExecutionFresh, report.ExecutionCached} {
		result, err := engine.Run(t.Context(), curate)
		if err != nil {
			t.Fatalf("curate: %v", err)
		}
		killed := mutantAt(t, result, string(mutation.StrCase), failfastWatchedSite)
		if killed.State != report.Killed || killed.RunsTruncated || killed.ExecutedRuns != 3 || len(killed.Diagnostics) != 3 {
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
	if !killed.RunsTruncated || killed.ExecutedRuns != 1 || killed.Provenance.Execution != report.ExecutionCached {
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
