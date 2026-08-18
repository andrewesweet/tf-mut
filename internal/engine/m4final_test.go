package engine_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// The M4.final outcome-table row sweep (#66): every status of the closed
// vocabulary is reachable through the engine seam, and each names the fixture
// that produces it. A status nobody can reach is a lie in the schema.

func TestEveryOutcomeTableRowIsReachableThroughTheSeam(t *testing.T) {
	t.Parallel()

	// The four generation-time rows, from dry runs over their fixtures.
	dryRunRows := map[report.SuggestionStatus]string{
		report.SuggestionCandidate:                suggestBasicFixture,
		report.SuggestionSkippedSensitive:         suggestSensitiveFixture,
		report.SuggestionSkippedUnaddressable:     suggestSensitiveFixture,
		report.SuggestionSkippedUnrenderable:      suggestSensitiveFixture,
		report.SuggestionSkippedUnsupportedTarget: suggestJSONFixture,
	}

	reached := map[report.SuggestionStatus]bool{}

	for _, fixture := range []string{
		suggestBasicFixture, suggestSensitiveFixture, suggestJSONFixture,
	} {
		result := runSuggest(t, dryRunConfig(t, copyFixture(t, fixture)))
		for status, count := range result.SuggestionsByStatus() {
			if count > 0 {
				reached[status] = true
			}
		}
	}

	for status, fixture := range dryRunRows {
		if !reached[status] {
			t.Errorf("status %s is unreachable; %s was expected to produce it", status, fixture)
		}
	}

	// The two verification-time rows: verified from the clean run, refuted
	// from the seeded defect.
	verified := runSuggest(t, suggestConfig(t, copyFixture(t, suggestBasicFixture)))
	if len(withStatus(verified, report.SuggestionVerified)) == 0 {
		t.Error("status verified is unreachable")
	}

	seeded := suggestConfig(t, copyFixture(t, suggestBasicFixture))
	seeded.SeedSuggestionDefect = suggest.DefectVacuous

	refuted := runSuggest(t, seeded)
	if len(withStatus(refuted, report.SuggestionRefuted)) == 0 {
		t.Error("status refuted is unreachable")
	}
}
