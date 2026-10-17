package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/oracle"
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
var seedSuggestionDefect = func(settings) suggest.Defect { return suggest.DefectNone }

// suggestibleDiagnoses names the diagnoses a suggestion is generated for.
//
// Each one names a proven delta; the adapters decide whether it is expressible.
// The indeterminate diagnoses name a comparison the oracle could not make,
// so there is nothing to assert; and
// `StructurallyUnassertable` is not a survivor diagnosis at all — its skeleton
// generation was removed from that milestone and relocated behind the
// minable-share measurement it always belonged to.
//
//nolint:gochecknoglobals // an immutable lookup table.
var suggestibleDiagnoses = []oracle.Diagnosis{
	oracle.NoAssertion, oracle.WeakAssertion, oracle.Unasserted,
}

// suggestAssertions is the suggestion leg's reaction to the classified
// population. It generates — and, unless this is a dry run, verifies — the
// assertion that would have killed each selected survivor and publishes the
// outcome of each survivor generation said anything about. A verifying run
// then publishes the verification cost, the executions actually spent on the
// candidates and on attributing any batch failures; a dry run spends none.
func suggestAssertions(
	ctx context.Context,
	plan executionPlan,
	evaluated evaluatedPopulation,
) error {
	suggestPhase := plan.collector.phase(phaseSuggest)
	defer suggestPhase()

	selected, err := selectSurvivors(plan.settings, evaluated.rows)
	if err != nil {
		return err
	}

	candidates, skipped := suggest.Generator{
		Configuration: plan.configuration,
		Schemas:       plan.prepared.schemas,
		Baseline:      plan.prepared.payloads,
		Defect:        seedSuggestionDefect(plan.settings),
	}.Generate(suggestable(selected, evaluated))

	if plan.settings.SuggestDryRun {
		return publishGeneration(plan.events, selected, candidates, nil, skipped)
	}

	verified, costs, err := verifySuggestions(ctx, plan, candidates)
	if err != nil {
		return err
	}

	if err := publishGeneration(plan.events, selected, nil, verified, skipped); err != nil {
		return err
	}

	return plan.events.verificationCosted.publish(VerificationCosted{costs: costs})
}

// publishGeneration publishes generation's and verification's outcomes in
// survivor order — the order generation produced them in — with collapsed
// duplicates carried by their first assertion's AlsoKills. A survivor the
// generator said nothing about publishes nothing.
func publishGeneration(
	events *dispatcher,
	selected []report.Mutant,
	candidates []suggest.Candidate,
	concluded []suggest.Suggestion,
	skipped []suggest.Suggestion,
) error {
	carriers := map[string]suggest.Candidate{}
	for _, candidate := range candidates {
		carriers[candidate.MutantID()] = candidate
	}

	terminals := map[string]suggest.Suggestion{}
	for _, suggestion := range concluded {
		terminals[suggestion.MutantID()] = suggestion
	}

	for _, suggestion := range skipped {
		terminals[suggestion.MutantID()] = suggestion
	}

	for _, mutant := range selected {
		if candidate, found := carriers[mutant.ID]; found {
			if err := events.suggestionProposed.publish(SuggestionProposed{candidate: candidate}); err != nil {
				return err
			}

			continue
		}

		if suggestion, found := terminals[mutant.ID]; found {
			if err := publishOutcome(events, suggestion); err != nil {
				return err
			}
		}
	}

	return nil
}

// publishOutcome publishes one terminal outcome under its own event.
func publishOutcome(events *dispatcher, suggestion suggest.Suggestion) error {
	if _, skipped := suggestion.SkipReason(); skipped {
		return events.suggestionSkipped.publish(SuggestionSkipped{suggestion: suggestion})
	}

	if suggestion.Verified() {
		return events.suggestionVerified.publish(SuggestionVerified{suggestion: suggestion})
	}

	return events.suggestionRefuted.publish(SuggestionRefuted{suggestion: suggestion})
}

// suggestable projects the selected survivors onto the Suggestion context's
// input: the mutant identifier and the proven delta, and nothing about the
// diagnosis — the engine grades, the context turns grading into improvement.
// A survivor whose diagnosis names no proven delta — the indeterminate
// comparisons — is not projected: there is nothing to assert about it.
// Keep the oracle's complete delta here: the report's display cap can hide a
// value change behind output metadata and must not constrain generation.
func suggestable(selected []report.Mutant, evaluated evaluatedPopulation) []suggest.Survivor {
	survivors := make([]suggest.Survivor, 0, len(selected))
	selectedIDs := make(map[string]bool, len(selected))

	for _, mutant := range selected {
		selectedIDs[mutant.ID] = true
	}

	for index, mutant := range evaluated.rows {
		outcome := evaluated.classified[index].Outcome
		if !selectedIDs[mutant.ID] || !slices.Contains(suggestibleDiagnoses, outcome.Diagnosis()) {
			continue
		}

		survivors = append(survivors,
			suggest.NewSurvivor(mutant.ID, outcome.Evidence().Delta()))
	}

	return survivors
}

// Statement states what verification actually executed: every
// full-suite run the baseline leg and its attribution executed, and every
// isolated mutant run.
func (c verificationCosts) Statement() string {
	if c.fullSuite == 0 && c.isolated == 0 {
		return ""
	}

	return fmt.Sprintf("verification executed %d full-suite run(s) plus %d isolated mutant run(s)",
		c.fullSuite, c.isolated)
}

// selectSurvivors narrows the population to the survivors the caller asked
// about, and refuses an identifier the population does not carry.
func selectSurvivors(settings settings, mutants []report.Mutant) ([]report.Mutant, error) {
	survivors := (report.Report{Mutants: mutants}).Survivors()

	if len(settings.SurvivorIDs) == 0 {
		return survivors, nil
	}

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
func checkSuggestCombinations(settings settings) error {
	if settings.mode != suggestMode {
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
