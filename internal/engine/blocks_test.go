package engine_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The report is one union document, and the blocks a command may or may not
// produce are four: the gate table, the suggestion outcome table, the apply
// record and the characterisation block. Which of the four a report carries
// is decided by the command alone — the presence of a characterisation block
// means a characterisation ran, a suggestion table means suggest ran — so
// each request type's projection is pinned to its own blocks here, one test
// per command. The assertions are structural, about which blocks are present
// and absent; the behaviour each block's content is contract for is asserted
// by the per-milestone suites and is not restated here.

// assertCarriesNoSuggestions fails the test when the report carries a
// suggestion, which only the suggest command produces.
func assertCarriesNoSuggestions(t *testing.T, result report.Report) {
	t.Helper()

	if len(result.Suggestions) != 0 {
		t.Fatalf("the report carries %d suggestion(s), which only the suggest command produces",
			len(result.Suggestions))
	}
}

// assertCarriesNoApplyRecord fails the test when the report carries an apply
// record, which only a suggest invocation asked to apply produces.
func assertCarriesNoApplyRecord(t *testing.T, result report.Report) {
	t.Helper()

	if result.Apply != nil {
		t.Fatal("the report carries an apply record, which only a suggest invocation asked to apply produces")
	}
}

// assertCarriesNoCharacterisation fails the test when the report carries a
// characterisation block, whose presence would say a characterisation ran.
func assertCarriesNoCharacterisation(t *testing.T, result report.Report) {
	t.Helper()

	if result.Characterisation != nil {
		t.Fatal("the report carries a characterisation block, whose presence means a characterisation ran")
	}
}

// TestARunReportCarriesNoSuggestionsNoApplyRecordAndNoCharacterisation pins
// the run projection to the blocks a run produces: the grading result and the
// gate table, and none of the generation direction's blocks.
func TestARunReportCarriesNoSuggestionsNoApplyRecordAndNoCharacterisation(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "skeleton")

	if result.Gates == nil {
		t.Fatal("a run grades its population, so the report carries the gate table")
	}

	assertCarriesNoSuggestions(t, result)
	assertCarriesNoApplyRecord(t, result)
	assertCarriesNoCharacterisation(t, result)
}

// TestAPreviewReportCarriesNoGates pins the preview projection to none of the
// four: a preview executes nothing, so there is no gate table to report, and
// it generates no suggestions, applies nothing and characterises nothing.
func TestAPreviewReportCarriesNoGates(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), previewRequest(t, copyFixture(t, "skeleton")))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if result.Gates != nil {
		t.Fatal("a preview executes nothing, so the report carries no gate table")
	}

	assertCarriesNoSuggestions(t, result)
	assertCarriesNoApplyRecord(t, result)
	assertCarriesNoCharacterisation(t, result)
}

// TestASuggestReportCarriesNoApplyRecordAndNoCharacterisation pins the
// suggest projection to its own blocks: the suggestion outcome table over the
// graded population's gate table, with no characterisation block and no
// apply record where the caller asked for none.
func TestASuggestReportCarriesNoApplyRecordAndNoCharacterisation(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestBasicFixture)))

	if len(result.Suggestions) == 0 {
		t.Fatal("suggest generates the assertion outcomes, so the report carries them")
	}

	if result.Gates == nil {
		t.Fatal("suggest grades its population, so the report carries the gate table")
	}

	assertCarriesNoApplyRecord(t, result)
	assertCarriesNoCharacterisation(t, result)
}

// TestACharacterisationReportCarriesNoGatesNoSuggestionsAndNoApplyRecord pins
// the characterise projection to its own block: the scaffold, its pins and
// its judgement points, with none of the grading direction's blocks.
func TestACharacterisationReportCarriesNoGatesNoSuggestionsAndNoApplyRecord(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(),
		characteriseRequest(t, copyFixture(t, untestedSensitiveFixture)))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	if result.Characterisation == nil {
		t.Fatal("a characterisation ran, so the report carries its block")
	}

	if result.Gates != nil {
		t.Fatal("a characterisation grades no population, so the report carries no gate table")
	}

	assertCarriesNoSuggestions(t, result)
	assertCarriesNoApplyRecord(t, result)
}

// TestACurateReportCarriesACharacterisationBlockAndNoSuggestions pins the
// curate projection to its own blocks: the characterisation block carrying
// the kill-set findings, over the gate table of the population it executed,
// with none of the suggestion engine's blocks.
func TestACurateReportCarriesACharacterisationBlockAndNoSuggestions(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), curateRequest(t, copyFixture(t, curateFixture)))
	if err != nil {
		t.Fatalf("curate: %v", err)
	}

	if result.Characterisation == nil {
		t.Fatal("curate reports its findings in the characterisation block, so the report carries it")
	}

	if result.Gates == nil {
		t.Fatal("curate executes the full population and honours the gate flags, so the report carries the gate table")
	}

	assertCarriesNoSuggestions(t, result)
	assertCarriesNoApplyRecord(t, result)
}

// TestATodosReportCarriesACharacterisationBlockAndNoSuggestions pins the
// todos projection to its own block: the open judgement points, with none of
// the blocks a command that runs Terraform produces.
func TestATodosReportCarriesACharacterisationBlockAndNoSuggestions(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), todosRequest(t, copyFixture(t, untestedTodoFixture)))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	if result.Characterisation == nil {
		t.Fatal("todos lists its judgement points in the characterisation block, so the report carries it")
	}

	if result.Gates != nil {
		t.Fatal("todos runs no Terraform, so the report carries no gate table")
	}

	assertCarriesNoSuggestions(t, result)
	assertCarriesNoApplyRecord(t, result)
}
