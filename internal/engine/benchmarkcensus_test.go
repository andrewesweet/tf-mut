package engine_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M5-0.4 benchmark-corpus census (#155): the module-admission measurement
// over Oasis's pinned 23-repository corpus. The live census is
// TestTheBenchmarkCorpusCensus (integration-tagged, network-gated); this file
// holds the row vocabulary, the two per-invocation classifiers and the
// offline gate fixtures the review passes promoted, which together prove the
// census's central discipline: population availability and row outcome are
// decided by two separate invocations, never inferred from each other.
//
// The row vocabulary is M5d's total one, decided by the run invocation's
// first failing stage: fetch/digest (operational, harness-level), discovery
// (unparseable-source, unsupported-construct), the safety gates
// (real-infrastructure, unsandboxed-effects), the run-block check (no-suite),
// init and schema (operational), the baseline (red-baseline,
// unsupported-payload-version, no-suite), execution (scored-incomplete,
// scored).

// censusRow is one module's published row outcome from the total vocabulary.
type censusRow string

const (
	rowScored                    censusRow = "scored"
	rowScoredIncomplete          censusRow = "scored-incomplete"
	rowUnparseableSource         censusRow = "unparseable-source"
	rowUnsupportedConstruct      censusRow = "unsupported-construct"
	rowRealInfrastructure        censusRow = "real-infrastructure"
	rowUnsandboxedEffects        censusRow = "unsandboxed-effects"
	rowNoSuite                   censusRow = "no-suite"
	rowRedBaseline               censusRow = "red-baseline"
	rowUnsupportedPayloadVersion censusRow = "unsupported-payload-version"
	rowOperational               censusRow = "operational"
)

// populationFact is one module's population availability: known when the
// module's own preview invocation exited zero with a report, unknown
// otherwise with that invocation's refusal text as the reason.
type populationFact struct {
	Known bool
	Size  int
	// Reason is preview's refusal text, published verbatim, when unknown.
	Reason string
}

// classifyAvailability decides availability from the preview invocation's own
// outcome and nothing else. A preview that exits zero with a report makes the
// population known at the size the report publishes; anything else is
// unknown, with the error's text as the reason. The reason is deliberately
// not mapped onto the row vocabulary: availability is a fact about one
// invocation, not a verdict about the module.
func classifyAvailability(previewErr error, previewResult report.Report) populationFact {
	if previewErr != nil {
		return populationFact{Known: false, Reason: previewErr.Error()}
	}

	return populationFact{Known: true, Size: len(previewResult.Mutants)}
}

// classifyRow decides the row outcome from the run invocation's own outcome
// and nothing else, in M5d's seven-stage order. A run that completed is
// scored only when its population is fully observed — no timeouts, no
// unevaluable mutants, the same rule newAuthoritativePopulation applies —
// and scored-incomplete otherwise. An error no stage sentinel claims is
// operational, which the census retries once and publishes as a fact about
// this run rather than the module.
func classifyRow(runErr error, runResult report.Report) censusRow {
	switch {
	case runErr == nil:
		if runResult.Count(report.Timeout) == 0 && len(runResult.Errors) == 0 {
			return rowScored
		}

		return rowScoredIncomplete
	case errors.Is(runErr, discovery.ErrParse):
		return rowUnparseableSource
	case errors.Is(runErr, discovery.ErrUnmodelledConstruct):
		return rowUnsupportedConstruct
	case errors.Is(runErr, engine.ErrRealInfrastructure):
		return rowRealInfrastructure
	case errors.Is(runErr, engine.ErrUnsandboxedEffects):
		return rowUnsandboxedEffects
	case errors.Is(runErr, engine.ErrBaselineNoRuns):
		return rowNoSuite
	case errors.Is(runErr, engine.ErrBaselineRed):
		return rowRedBaseline
	case errors.Is(runErr, engine.ErrTerraformVersion):
		return rowUnsupportedPayloadVersion
	default:
		return rowOperational
	}
}

// censusRun is the census's run invocation: --no-cache --tier standard,
// through the seam. The preview invocation is previewRequest with the same
// common; preview is cache-free by construction, and the harness sets
// NoCache on it only to match the published invocation line.
func censusRun(t *testing.T, moduleDir string) engine.RunRequest {
	t.Helper()

	request := baseConfig(t, moduleDir)
	request.NoCache = true
	request.Tier = "standard"

	return request
}

// TestTheThreePreviewRefusalsLeaveThePopulationUnknown pins the promoted
// review fixtures: each refusal module's preview invocation exits with an
// error and no report, so the population is unknown with the refusal text.
// Each is decided by the preview invocation alone.
func TestTheThreePreviewRefusalsLeaveThePopulationUnknown(t *testing.T) {
	t.Parallel()

	for name := range map[string]string{
		"preview-refusal-nosuite":     "baseline executed no run blocks",
		"preview-refusal-import":      "configuration declares a construct this version does not model",
		"preview-refusal-unparseable": "configuration could not be parsed",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, name)
			request := previewRequest(t, module)

			result, err := engine.Run(t.Context(), request)
			if err == nil {
				t.Fatalf("preview exited zero with a report of %d mutants", len(result.Mutants))
			}

			fact := classifyAvailability(err, result)
			if fact.Known {
				t.Fatal("the population is known on a preview refusal")
			}

			if !strings.Contains(fact.Reason, wantReason(name)) {
				t.Fatalf("refusal reason %q does not name the refusal", fact.Reason)
			}
		})
	}
}

func wantReason(fixture string) string {
	switch fixture {
	case "preview-refusal-nosuite":
		return "baseline executed no run blocks"
	case "preview-refusal-import":
		return "configuration declares a construct this version does not model"
	default:
		return "configuration could not be parsed"
	}
}

// TestTheGatedPreviewFixtureHasAKnownPopulationAndAnUnsandboxedEffectsRow
// pins the second promoted fixture: the same module's preview succeeds with
// six mutants while its run is refused at the safety gate. Availability and
// row outcome are decided by different invocations and the combination is
// the point: a known population and a refused row coexist.
func TestTheGatedPreviewFixtureHasAKnownPopulationAndAnUnsandboxedEffectsRow(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "gated-preview")

	previewResult, previewErr := engine.Run(t.Context(), previewRequest(t, module))
	if previewErr != nil {
		t.Fatalf("preview refused: %v", previewErr)
	}

	fact := classifyAvailability(previewErr, previewResult)
	if !fact.Known {
		t.Fatalf("the population is unknown: %s", fact.Reason)
	}

	if fact.Size != 6 {
		t.Fatalf("the population is %d mutants, want the pinned 6", fact.Size)
	}

	_, runErr := engine.Run(t.Context(), censusRun(t, module))
	if runErr == nil {
		t.Fatal("run exited zero where the safety gate refuses")
	}

	row := classifyRow(runErr, report.Report{})
	if row != rowUnsandboxedEffects {
		t.Fatalf("row outcome %q, want %q", row, rowUnsandboxedEffects)
	}
}

// TestAPreviewThatFailsOperationallyWhileTheRunSucceeds pins the one
// combination the ticket names explicitly: the row outcome is decided by the
// run invocation even when the preview invocation failed operationally, so
// an operational preview can never mask a scorable module. The preview
// failure is staged at the binary, which is an operational refusal no stage
// sentinel claims.
func TestAPreviewThatFailsOperationallyWhileTheRunSucceeds(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	broken := previewRequest(t, module)
	broken.TerraformBinary = "/nonexistent/tf-mut-census-binary"

	previewResult, previewErr := engine.Run(t.Context(), broken)
	if previewErr == nil {
		t.Fatal("preview exited zero through a nonexistent binary")
	}

	fact := classifyAvailability(previewErr, previewResult)
	if fact.Known || !strings.Contains(fact.Reason, "/nonexistent/tf-mut-census-binary") {
		t.Fatalf("availability did not record the operational refusal: %+v", fact)
	}

	if classifyRow(previewErr, previewResult) != rowOperational {
		t.Fatal("a binary-level refusal is not an operational row")
	}

	runResult, runErr := engine.Run(t.Context(), censusRun(t, module))
	if runErr != nil {
		t.Fatalf("run failed where the module scores: %v", runErr)
	}

	if row := classifyRow(runErr, runResult); row != rowScored {
		t.Fatalf("row outcome %q, want %q", row, rowScored)
	}
}

// TestTheRowVocabularyMapsEveryStageSentinel pins the classifier's total
// mapping in M5d's seven-stage order, with the two completeness branches of
// a completed run. The vocabulary is total: every outcome the engine seam
// can produce lands on exactly one row.
func TestTheRowVocabularyMapsEveryStageSentinel(t *testing.T) {
	t.Parallel()

	completed := report.Report{}
	if classifyRow(nil, completed) != rowScored {
		t.Fatal("a completed, fully observed run is not scored")
	}

	timedOut := completed
	timedOut.Metrics.Counts = map[report.State]int{report.Timeout: 1}
	if classifyRow(nil, timedOut) != rowScoredIncomplete {
		t.Fatal("a run with a timeout is not scored-incomplete")
	}

	unevaluable := completed
	unevaluable.Errors = []report.ExecutionError{{MutantID: "m"}}
	if classifyRow(nil, unevaluable) != rowScoredIncomplete {
		t.Fatal("a run with unevaluable mutants is not scored-incomplete")
	}

	for _, mapped := range []struct {
		want     censusRow
		sentinel error
	}{
		{rowUnparseableSource, discovery.ErrParse},
		{rowUnsupportedConstruct, discovery.ErrUnmodelledConstruct},
		{rowRealInfrastructure, engine.ErrRealInfrastructure},
		{rowUnsandboxedEffects, engine.ErrUnsandboxedEffects},
		{rowNoSuite, engine.ErrBaselineNoRuns},
		{rowRedBaseline, engine.ErrBaselineRed},
		{rowUnsupportedPayloadVersion, engine.ErrTerraformVersion},
	} {
		if got := classifyRow(mapped.sentinel, report.Report{}); got != mapped.want {
			t.Errorf("sentinel %v classified %q, want %q", mapped.sentinel, got, mapped.want)
		}

		// Classification is identity-based (errors.Is), never textual: a
		// wrapped sentinel keeps its row.
		if got := classifyRow(fmt.Errorf("census: %w", mapped.sentinel), report.Report{}); got != mapped.want {
			t.Errorf("a wrapped sentinel classified %q, want %q", got, mapped.want)
		}
	}

	if got := classifyRow(os.ErrNotExist, report.Report{}); got != rowOperational {
		t.Errorf("an unclaimed error classified %q, want %q", got, rowOperational)
	}
}
