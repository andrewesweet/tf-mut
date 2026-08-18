package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// The suggestion-soundness gate (M4b.1). Both verification legs, both seeded
// defects, and the attribution the isolated leg is what proves.

func TestVerifiedRequiresBothLegsAndCarriesTheirEvidence(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestConfig(t, copyFixture(t, suggestBasicFixture)))

	verified := withStatus(result, report.SuggestionVerified)
	if len(verified) == 0 {
		t.Fatalf("nothing verified; statuses were %s", suggest.Statuses(result.Suggestions))
	}

	for _, suggestion := range verified {
		if suggestion.Verification == nil {
			t.Fatalf("verified suggestion %s carries no evidence", suggestion.ID)
		}

		if !suggestion.Verification.Baseline.Passed {
			t.Fatalf("suggestion %s is verified with a failed baseline leg", suggestion.ID)
		}

		if !suggestion.Verification.Mutant.Passed {
			t.Fatalf("suggestion %s is verified with a failed mutant leg", suggestion.ID)
		}

		if len(suggestion.Verification.Baseline.Runs) == 0 ||
			len(suggestion.Verification.Mutant.Runs) == 0 {
			t.Fatalf("suggestion %s carries no run references", suggestion.ID)
		}

		if suggestion.VerifiedDigest == "" {
			t.Fatalf("verified suggestion %s carries no source digest", suggestion.ID)
		}
	}
}

// TestASeededWrongValueIsRefutedThroughTheBaselineLeg is the first half of the
// gate: an assertion that breaks the suite must never reach `verified`.
func TestASeededWrongValueIsRefutedThroughTheBaselineLeg(t *testing.T) {
	t.Parallel()

	config := suggestConfig(t, copyFixture(t, suggestBasicFixture))
	config.SeedSuggestionDefect = suggest.DefectWrongValue

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) == 0 {
		t.Fatalf("the seeded wrong value was not refuted: %s", suggest.Statuses(result.Suggestions))
	}

	for _, suggestion := range refuted {
		if suggestion.Verification.Baseline.Passed {
			t.Fatalf("suggestion %s was refuted but its baseline leg passed", suggestion.ID)
		}

		if suggestion.StatusReason == "" {
			t.Fatalf("refuted suggestion %s carries no reason", suggestion.ID)
		}
	}
}

// TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg is the second half,
// and the attribution proof with it: the vacuous assertion is applied beside
// real ones that do kill its mutant, so only the isolated check can see that it
// does not kill anything itself.
func TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg(t *testing.T) {
	t.Parallel()

	config := suggestConfig(t, copyFixture(t, suggestBasicFixture))
	config.SeedSuggestionDefect = suggest.DefectVacuous

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) != 1 {
		for _, suggestion := range result.Suggestions {
			t.Logf("%s %s expr=%q reason=%q verification=%+v", suggestion.ID, suggestion.Status,
				suggestion.Expression, suggestion.StatusReason, suggestion.Verification)
		}

		t.Fatalf("want exactly the seeded vacuous suggestion refuted, got %s",
			suggest.Statuses(result.Suggestions))
	}

	for _, suggestion := range result.Suggestions {
		t.Logf("%s %s expr=%q reason=%q", suggestion.ID, suggestion.Status,
			suggestion.Expression, suggestion.StatusReason)
	}

	seeded := refuted[0]

	left, right, isEquality := strings.Cut(seeded.Expression, " == ")
	if !isEquality || left != right {
		t.Fatalf("the refuted suggestion is not the seeded tautology: %q", seeded.Expression)
	}

	if !seeded.Verification.Baseline.Passed {
		t.Fatal("the vacuous assertion should keep the suite green; it did not")
	}

	if seeded.Verification.Mutant.Passed {
		t.Fatal("the vacuous assertion was credited with a kill it did not make")
	}

	if len(withStatus(result, report.SuggestionVerified)) == 0 {
		t.Fatal("no real suggestion verified beside the seeded one, so nothing could have " +
			"laundered it and the attribution claim is untested")
	}
}

func TestASuggestExitCodeIsOneOnlyWhenSomethingIsRefuted(t *testing.T) {
	t.Parallel()

	clean := runSuggest(t, suggestConfig(t, copyFixture(t, suggestBasicFixture)))
	if code := clean.ExitCode(report.Gate{}); code != report.ExitClean { //nolint:exhaustruct // no gate.
		t.Fatalf("exit code = %d, want %d when every suggestion concluded",
			code, report.ExitClean)
	}

	config := suggestConfig(t, copyFixture(t, suggestBasicFixture))
	config.SeedSuggestionDefect = suggest.DefectVacuous

	refuted := runSuggest(t, config)
	if code := refuted.ExitCode(report.Gate{}); code != report.ExitFindings { //nolint:exhaustruct // no gate.
		t.Fatalf("exit code = %d, want %d when a suggestion was refuted", code, report.ExitFindings)
	}
}

func TestAStaleSurvivorIdentifierIsAnOperationalFailureNamingIt(t *testing.T) {
	t.Parallel()

	config := suggestConfig(t, copyFixture(t, suggestBasicFixture))
	config.SurvivorIDs = []string{"000000000000", "111111111111"}

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrSurvivorSelection) {
		t.Fatalf("error = %v, want a survivor-selection failure", err)
	}

	for _, id := range config.SurvivorIDs {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("the failure does not name %s: %v", id, err)
		}
	}
}

func TestSurvivorSelectionScopesTheSuggestions(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestBasicFixture)

	all := runSuggest(t, dryRunConfig(t, module))
	if len(all.Suggestions) < 2 {
		t.Fatalf("the fixture produced %d suggestions; the scoping claim needs more than one",
			len(all.Suggestions))
	}

	config := dryRunConfig(t, module)
	config.SurvivorIDs = []string{all.Suggestions[0].MutantID}

	scoped := runSuggest(t, config)
	if len(scoped.Suggestions) != 1 {
		t.Fatalf("scoped run produced %d suggestions, want 1", len(scoped.Suggestions))
	}

	if scoped.Suggestions[0].MutantID != all.Suggestions[0].MutantID {
		t.Fatalf("scoped run suggested for %s, want %s",
			scoped.Suggestions[0].MutantID, all.Suggestions[0].MutantID)
	}
}

// TestVerificationIsNeverCached: the second run replays mutant verdicts from
// the cache and still verifies every suggestion afresh, with its own evidence.
func TestVerificationIsNeverCached(t *testing.T) {
	t.Parallel()

	config := suggestConfig(t, copyFixture(t, suggestBasicFixture))

	first := runSuggest(t, config)
	second := runSuggest(t, config)
	if second.Population.Cached == 0 {
		t.Fatal("the second run cached no verdicts, so the claim is untested")
	}

	if len(withStatus(second, report.SuggestionVerified)) !=
		len(withStatus(first, report.SuggestionVerified)) {
		t.Fatalf("the cached run verified %d suggestions, the fresh run %d",
			len(withStatus(second, report.SuggestionVerified)),
			len(withStatus(first, report.SuggestionVerified)))
	}

	for _, suggestion := range withStatus(second, report.SuggestionVerified) {
		if suggestion.Verification == nil || len(suggestion.Verification.Mutant.Runs) == 0 {
			t.Fatalf("suggestion %s carries no freshly executed evidence", suggestion.ID)
		}
	}
}

// TestAScopedSuggestRunStatesItsVerificationCost is US14's bounded-and-chosen
// half: the report states what the verification contract executed — one
// full-suite run per target file, one isolated run per candidate.
func TestAScopedSuggestRunStatesItsVerificationCost(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestBasicFixture)

	dry := runSuggest(t, dryRunConfig(t, module))
	if len(dry.Suggestions) == 0 {
		t.Fatal("no suggestions to scope to")
	}

	config := suggestConfig(t, module)
	config.SurvivorIDs = []string{dry.Suggestions[0].MutantID}

	result := runSuggest(t, config)

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "verification executed 1 full-suite run(s)") &&
			strings.Contains(warning, "1 isolated mutant run(s)") {
			return
		}
	}

	t.Fatalf("the scoped run does not state its verification cost: %v", result.Warnings)
}
