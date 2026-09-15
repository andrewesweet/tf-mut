package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// ErrSurvivorSelection reports a `--survivor` identifier the population does
// not carry, or carries in a state that has nothing to suggest.
//
// It is an operational failure and never a suggestion outcome: a caller who
// named a mutant that is gone is working from a stale report, and answering
// with an empty result would let that go unnoticed.
var ErrSurvivorSelection = errors.New("no survivor with that identifier")

//nolint:gochecknoglobals // inert production default; external tests scope and restore it.
var seedSuggestionDefect = func(Config) suggest.Defect { return suggest.DefectNone }

// suggestibleDiagnoses names the diagnoses a suggestion is generated for.
//
// Each one names a proven, expressible delta. The indeterminate diagnoses name
// a comparison the oracle could not make, so there is nothing to assert; and
// `StructurallyUnassertable` is not a survivor diagnosis at all — its skeleton
// generation was removed from that milestone and relocated behind the
// minable-share measurement it always belonged to.
//
//nolint:gochecknoglobals // an immutable lookup table.
var suggestibleDiagnoses = []report.Diagnosis{
	report.NoAssertion, report.WeakAssertion, report.Unasserted,
}

// suggestAssertions generates — and, unless this is a dry run, verifies — the
// assertion that would have killed each selected survivor. The second result
// is the verification cost statement: verification is bounded and chosen, so
// what it will execute is stated in the report rather than discovered on the
// bill.
func suggestAssertions(
	ctx context.Context,
	plan executionPlan,
	result report.Report,
) ([]report.Suggestion, string, error) {
	selected, err := selectSurvivors(plan.config, result)
	if err != nil {
		return nil, "", err
	}

	candidates, skipped := suggest.Generator{
		Configuration: plan.configuration,
		Schemas:       plan.prepared.schemas,
		Defect:        seedSuggestionDefect(plan.config),
	}.Generate(suggestable(selected))

	if plan.config.SuggestDryRun {
		return projectGeneration(selected, candidates, nil, skipped), "", nil
	}

	verified, err := verifySuggestions(ctx, plan, candidates)
	if err != nil {
		return nil, "", err
	}

	return projectGeneration(selected, nil, verified, skipped), verificationCost(candidates), nil
}

// suggestable projects the selected survivors onto the Suggestion context's
// input: the mutant identifier and the proven delta, and nothing about the
// diagnosis — the engine grades, the context turns grading into improvement.
// A survivor whose diagnosis names no proven delta — the indeterminate
// comparisons — is not projected: there is nothing to assert about it.
func suggestable(selected []report.Mutant) []suggest.Survivor {
	survivors := make([]suggest.Survivor, 0, len(selected))

	for _, mutant := range selected {
		if mutant.Verdict == nil || !slices.Contains(suggestibleDiagnoses, mutant.Verdict.Diagnosis) {
			continue
		}

		survivors = append(survivors,
			suggest.NewSurvivor(mutant.ID, projectSurvivorDelta(mutant.Verdict.Evidence.Delta)))
	}

	return survivors
}

// projectSurvivorDelta hands the generator the report's delta in the
// fingerprint document shape the adapters read. It is the same capped delta
// the report carries: generation sees what the report showed, not more.
func projectSurvivorDelta(changes []report.Change) []fingerprint.Change {
	delta := make([]fingerprint.Change, 0, len(changes))

	for _, change := range changes {
		delta = append(delta, fingerprint.Change{
			Run:       change.Run,
			Path:      change.Path,
			Address:   change.Address,
			Baseline:  change.Baseline,
			Mutant:    change.Mutant,
			Sensitive: change.Sensitive,
		})
	}

	return delta
}

// verificationCost states what the verification contract executes for a
// candidate set: one full-suite run per target test file, plus one isolated
// mutant run per candidate.
func verificationCost(candidates []suggest.Candidate) string {
	candidatesCount := 0
	files := map[string]bool{}

	for _, candidate := range candidates {
		candidatesCount += 1 + len(candidate.AlsoKills())
		files[candidate.TargetFile()] = true
	}

	if candidatesCount == 0 {
		return ""
	}

	return fmt.Sprintf("verification executed %d full-suite run(s) — one per target test "+
		"file — plus %d isolated mutant run(s), one per mutant a suggestion claims",
		len(files), candidatesCount)
}

// selectSurvivors narrows the population to the survivors the caller asked
// about, and refuses an identifier the population does not carry.
func selectSurvivors(settings Config, result report.Report) ([]report.Mutant, error) {
	if len(settings.SurvivorIDs) == 0 {
		return result.Survivors(), nil
	}

	survivors := result.Survivors()
	present := map[string]bool{}

	for _, survivor := range survivors {
		present[survivor.ID] = true
	}

	missing := []string{}

	for _, id := range settings.SurvivorIDs {
		if !present[id] {
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)

		return nil, fmt.Errorf("%w: %s is not a survivor of this population; "+
			"the report it came from is stale, so re-run before selecting again",
			ErrSurvivorSelection, strings.Join(missing, ", "))
	}

	selected := []report.Mutant{}

	for _, survivor := range survivors {
		if slices.Contains(settings.SurvivorIDs, survivor.ID) {
			selected = append(selected, survivor)
		}
	}

	return selected, nil
}

// ErrSuggestCombination reports a suggest flag combination whose halves
// contradict each other.
var ErrSuggestCombination = errors.New("contradictory suggest flags")

// checkSuggestCombinations refuses, before any work is done, the combinations
// the outcome model has no honest answer for (round-3 review, PR #69):
//
//   - --dry-run with --apply/--all-verified: a dry run verifies nothing, so no
//     suggestion can be verified, and "applied 0 suggestion(s)" would report a
//     successful apply of nothing the caller plainly asked for.
//   - --since/test selection with suggest: verification runs the full suite by
//     contract, so a filtered population would let an excluded run's kill be
//     attributed to a suggestion — the exact laundering the isolated leg
//     exists to prevent.
func checkSuggestCombinations(settings Config) error {
	if !settings.Suggest {
		return nil
	}

	if settings.SuggestDryRun && (settings.ApplyAll || len(settings.Apply) > 0) {
		return fmt.Errorf("%w: --dry-run verifies nothing, so nothing could be verified "+
			"for --apply to write; drop one of the two", ErrSuggestCombination)
	}

	if len(settings.TestSelection) > 0 {
		return fmt.Errorf("%w: verification runs the full suite by contract, and a test "+
			"selection would let an excluded run's kill be misattributed", ErrSuggestCombination)
	}

	return nil
}
