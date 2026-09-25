//go:build integration

package engine_test

import (
	"slices"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestTheShippedSecurityAWSPackIsWitnessedOnAWSMocked is the M5c.2 acceptance
// witness: `--pack security-aws`, with no registration anywhere, on preview,
// run and suggest — one invocation each — over the checked-in aws-mocked
// fixture. Every admitted entry must appear in the origins of the row its
// bytes collapsed onto, no attributed mutant may be Invalid, every no-pack
// row must survive under the same identifier (the pack adds origins for rows
// a language operator owns), the population may grow by exactly the rows the
// pack operators own — the widen-cidr entry's bytes no language operator
// produces — and every entry must be reachable from a verified suggestion.
//
//nolint:paralleltest // shares the integration provider cache with the other legs.
func TestTheShippedSecurityAWSPackIsWitnessedOnAWSMocked(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)

	// Preview: every no-pack row survives, and the growth is exactly the rows
	// the pack operators own: the flip entries' bytes are owned by
	// BOOL-LITERAL-FLIP, so they ride existing rows as origins, while the
	// widen-cidr entry's `0.0.0.0/0` is a rewrite no language operator
	// produces, so its row is new.
	network := networkConfig(t, module)

	preview := previewRequest(t, module)
	preview.Common = network.Common
	preview.Packs = []string{shippedSecurityAWSName}

	withPack, err := engine.Run(t.Context(), preview)
	if err != nil {
		t.Fatalf("preview with the shipped pack: %v", err)
	}

	preview.Packs = nil

	withoutPack, err := engine.Run(t.Context(), preview)
	if err != nil {
		t.Fatalf("preview without the pack: %v", err)
	}

	ownedRows := 0
	for _, mutant := range withPack.Mutants {
		if mutant.Operator == string(mutation.PackWidenCIDR) && mutant.Tier == string(mutation.TierPack) {
			ownedRows++
		}
	}

	if len(withPack.Mutants) != len(withoutPack.Mutants)+ownedRows {
		t.Fatalf("the shipped pack changed the population by %d, want exactly its %d owned rows: %d mutants, was %d",
			len(withPack.Mutants)-len(withoutPack.Mutants), ownedRows, len(withPack.Mutants), len(withoutPack.Mutants))
	}

	seen, invalid := collectShippedOrigins(t, withPack)

	for _, entry := range shippedSecurityAWSEntries() {
		if !seen[entry] {
			t.Errorf("shipped entry %q has no aws-mocked witness", entry)
		}

		if invalid[entry] {
			t.Errorf("shipped entry %q re-executed as Invalid", entry)
		}
	}

	// Run: the same witness graded, cache off, still with zero invalid.
	run := networkConfig(t, module)
	run.NoCache = true
	run.Packs = []string{shippedSecurityAWSName}

	graded, err := engine.Run(t.Context(), run)
	if err != nil {
		t.Fatalf("run with the shipped pack: %v", err)
	}

	_, runInvalid := collectShippedOrigins(t, graded)

	for entry, wentInvalid := range runInvalid {
		if wentInvalid {
			t.Errorf("run: shipped entry %q is Invalid", entry)
		}
	}

	// Suggest: every shipped entry is reachable from a verified suggestion —
	// the entry's own survivor the suggestion's assertion kills, or another
	// survivor the same assertion also kills. The fixture asserts neither the
	// KMS rotation, the public-access flags nor the egress CIDR, so their
	// rows are unasserted faults; a verified assertion at the site typically
	// kills the language operator's row and the pack-attributed row together.
	suggest := suggestRequest(t, module)
	suggest.Common = network.Common
	suggest.Packs = []string{shippedSecurityAWSName}
	suggest.NoCache = true

	suggested, err := engine.Run(t.Context(), suggest)
	if err != nil {
		t.Fatalf("suggest with the shipped pack: %v", err)
	}

	reached := mutantsAVerifiedSuggestionKills(graded, suggested)

	for _, mutant := range graded.Mutants {
		if !carriesAShippedPackOrigin(mutant) {
			continue
		}

		if !reached[mutant.ID] {
			t.Errorf("no verified suggestion kills %s at %s, which carries origins %+v",
				mutant.ID, mutant.Site, mutant.Origins)
		}
	}
}

// collectShippedOrigins walks one report's mutants and records, per shipped
// pack entry, whether it was witnessed and whether the row it rode was
// Invalid. Every origin must name the shipped pack.
func collectShippedOrigins(t *testing.T, result report.Report) (seen, invalid map[string]bool) {
	t.Helper()

	seen = map[string]bool{}
	invalid = map[string]bool{}

	for _, mutant := range result.Mutants {
		for _, origin := range mutant.Origins {
			if origin.Pack != shippedSecurityAWSName {
				t.Fatalf("origins name pack %q, want the shipped security-aws", origin.Pack)
			}

			seen[origin.Entry] = true
			invalid[origin.Entry] = invalid[origin.Entry] || mutant.State == report.Invalid
		}
	}

	return seen, invalid
}

// mutantsAVerifiedSuggestionKills collects the identifiers of every mutant a
// verified suggestion kills: the suggestion's own target, or another mutant
// the same folded assertion also kills.
func mutantsAVerifiedSuggestionKills(graded, suggested report.Report) map[string]bool {
	killed := map[string]bool{}

	for _, suggestion := range suggested.Suggestions {
		if suggestion.Status != report.SuggestionVerified {
			continue
		}

		killed[suggestion.MutantID] = true

		for _, id := range suggestion.AlsoKills {
			if _, found := mutantByID(graded, id); found {
				killed[id] = true
			}
		}
	}

	return killed
}

// carriesAShippedPackOrigin reports whether a mutant's origins include the
// shipped pack.
func carriesAShippedPackOrigin(mutant report.Mutant) bool {
	return slices.ContainsFunc(mutant.Origins, func(origin report.Origin) bool {
		return origin.Pack == shippedSecurityAWSName
	})
}

// mutantByID finds a mutant by its identifier.
func mutantByID(result report.Report, id string) (report.Mutant, bool) {
	for _, mutant := range result.Mutants {
		if mutant.ID == id {
			return mutant, true
		}
	}

	return report.Mutant{}, false
}
