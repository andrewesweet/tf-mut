package oracle

import (
	"cmp"
	"slices"

	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// InvalidShareWarningThreshold is the judged share of Invalid mutants at or
// above which the score is incomplete and the engine warns.
// A healthy module classifies a fraction of a percent as Invalid.
const InvalidShareWarningThreshold = 0.25

// Metrics are the population arithmetic's result: the scored set and the
// three headline numbers computed from the classified population. The engine
// projects it onto the published report at the boundary.
type Metrics struct {
	MutationScore  float64
	AssertionScore float64
	Reachability   float64
	// Incomplete marks a score that is not trustworthy: a trip or timeout left
	// part of the population unobserved, or a non-empty population produced no
	// score at all, or at least InvalidShareWarningThreshold of the judged
	// (non-Ignored) population was rejected by terraform validate.
	Incomplete bool
	// Counts holds the number of mutants in each state.
	Counts map[State]int
	// Diagnoses holds the number of survivors carrying each diagnosis.
	Diagnoses map[Diagnosis]int
	// Scored is the size of the scored set.
	Scored int
}

// OperatorErrors counts what one operator produced, so that the selective
// validation question can be answered from data rather than from opinion.
type OperatorErrors struct {
	Operator mutation.Operator
	// Generated is the number of mutants the operator produced.
	Generated int
	// Invalid is the number terraform validate rejected.
	Invalid int
	// KilledByError is the number Terraform's evaluation caught.
	KilledByError int
	// ErrorRate is Invalid ÷ Generated.
	ErrorRate float64
}

// Classified pairs one outcome with the operator that generated its mutant.
// The operator is the catalogue's fact about generation; the outcome is the
// oracle's fact about the verdict; the per-operator error rate is about both
// together, which is why neither alone can carry the row.
type Classified struct {
	Operator mutation.Operator
	Outcome  Outcome
}

// scoredStates is the scored set: everything the population can be graded on.
//
// Invalid, Unobservable and Ignored are excluded and reported as counts, so a
// mutant nobody could have caught never lowers a score.
//
//nolint:gochecknoglobals // an immutable lookup table.
var scoredStates = []State{
	StateKilled, StateKilledByError, StateSurvived, StateStructurallyUnassertable, StateNoCoverage, StateTimeout,
}

// ComputeMetrics derives the state counts and the three headline metrics from
// the classified population the oracle assigned.
func ComputeMetrics(population Population) Metrics {
	counts := map[State]int{}
	diagnoses := map[Diagnosis]int{}

	for _, row := range population.Classified() {
		counts[row.Outcome.State()]++

		if row.Outcome.State() == StateSurvived && row.Outcome.Diagnosis() != "" {
			diagnoses[row.Outcome.Diagnosis()]++
		}
	}

	scored := 0
	for _, state := range scoredStates {
		scored += counts[state]
	}

	killed := counts[StateKilled]
	killedByError := counts[StateKilledByError]
	survived := counts[StateSurvived]
	unassertable := counts[StateStructurallyUnassertable]
	timeout := counts[StateTimeout]
	judged := len(population.Classified()) - counts[StateIgnored]
	invalid := counts[StateInvalid]

	return Metrics{
		MutationScore:  ratio(killed+killedByError, scored),
		AssertionScore: ratio(killed, killed+survived+unassertable+timeout),
		Reachability:   ratio(killed+killedByError+survived+timeout, scored),
		Incomplete: population.provenance.Tripped || population.provenance.Unexecuted > 0 || timeout > 0 ||
			(scored == 0 && len(population.Classified()) > 0) ||
			(judged > 0 && ratio(invalid, judged) >= InvalidShareWarningThreshold),
		Counts:    counts,
		Diagnoses: diagnoses,
		Scored:    scored,
	}
}

// ComputeOperatorErrors summarises generation quality per operator over the
// classified population.
func ComputeOperatorErrors(population Population) []OperatorErrors {
	byOperator := map[mutation.Operator]*OperatorErrors{}

	for _, row := range population.Classified() {
		entry, found := byOperator[row.Operator]
		if !found {
			entry = &OperatorErrors{
				Operator: row.Operator, Generated: 0, Invalid: 0, KilledByError: 0, ErrorRate: 0,
			}
			byOperator[row.Operator] = entry
		}

		entry.Generated++

		//nolint:exhaustive // only the two error states contribute to the counts.
		switch row.Outcome.State() {
		case StateInvalid:
			entry.Invalid++
		case StateKilledByError:
			entry.KilledByError++
		default:
		}
	}

	counts := make([]OperatorErrors, 0, len(byOperator))

	for _, entry := range byOperator {
		entry.ErrorRate = ratio(entry.Invalid, entry.Generated)
		counts = append(counts, *entry)
	}

	slices.SortFunc(counts, func(left, right OperatorErrors) int {
		return cmp.Compare(left.Operator, right.Operator)
	})

	return counts
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}

	return float64(numerator) / float64(denominator)
}
