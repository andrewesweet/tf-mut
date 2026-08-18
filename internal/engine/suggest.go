package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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

// suggestAssertions generates — and, unless this is a dry run, verifies — the
// assertion that would have killed each selected survivor.
func suggestAssertions(
	ctx context.Context,
	plan executionPlan,
	result report.Report,
) ([]report.Suggestion, error) {
	selected, err := selectSurvivors(plan.config, result)
	if err != nil {
		return nil, err
	}

	generated := suggest.Generator{
		Configuration: plan.configuration,
		Schemas:       plan.prepared.schemas,
		Defect:        plan.config.SeedSuggestionDefect,
	}.Generate(selected)

	if plan.config.SuggestDryRun {
		return generated, nil
	}

	return verifySuggestions(ctx, plan, generated)
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
