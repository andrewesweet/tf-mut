package engine

import (
	"fmt"
	"time"

	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The population arithmetic's input column.
//
// Every outcome the oracle assigned in this run was constructed at its
// classification site and projected onto the published mutant; the DTO carries
// the outcome's public record. complete() reads those records back through
// oracle.ParseRecord — the parsing constructor, which rebuilds an Outcome only
// where the constructors could have produced it — so the arithmetic is
// computed over the Oracle context's own type and no illegal record can reach
// the numbers. The reconstruction states exactly what each record proves —
// the recorded state, diagnosis, finding, evidence, diagnostics, suppression
// and budget — and the result feeds the arithmetic alone; the published
// mutant is never rewritten from it.

// populationMetrics derives the report's metrics and per-operator error rates
// from the executed population's recorded outcomes. A record the constructors
// could not have produced is an internal invariant broken, not a verdict:
// the run fails rather than report numbers an illegal row contributed to.
func populationMetrics(
	executed []report.Mutant,
	budget time.Duration,
) (report.Metrics, []report.OperatorErrors, error) {
	outcomes := make([]oracle.Outcome, 0, len(executed))
	classified := make([]oracle.Classified, 0, len(executed))

	for _, mutant := range executed {
		outcome, err := parsedOutcome(mutant, budget)
		if err != nil {
			return report.Metrics{}, nil, fmt.Errorf("mutant %s: %w", mutant.ID, err)
		}

		outcomes = append(outcomes, outcome)
		classified = append(classified, oracle.Classified{
			Operator: mutation.Operator(mutant.Operator),
			Outcome:  outcome,
		})
	}

	return projectMetrics(oracle.ComputeMetrics(outcomes)),
		projectOperatorErrors(oracle.ComputeOperatorErrors(classified)), nil
}

// parsedOutcome reads one mutant's published record back through the
// parsing constructor its state was decided by.
func parsedOutcome(mutant report.Mutant, budget time.Duration) (oracle.Outcome, error) {
	record, err := storedRecord(oracleState(mutant.State), mutant.Verdict)
	if err != nil {
		return oracle.Outcome{}, err
	}

	// The decision fields the finding DTO does not carry: the evaluation
	// failures a killed-by-error or invalid claim is made from, the decision
	// an ignored mutant records, and the budget a timeout was measured
	// against. Every other state's record carries none of these.
	record.Diagnostics = oracleDiagnostics(mutant.Diagnostics)
	if mutant.Suppression != nil {
		suppression := oracleSuppression(mutant.Suppression)
		record.Suppression = &suppression
	}

	record.Budget = budget

	return oracle.ParseRecord(record)
}

// projectMetrics spells the arithmetic's result onto the published report.
func projectMetrics(metrics oracle.Metrics) report.Metrics {
	counts := make(map[report.State]int, len(metrics.Counts))
	for state, count := range metrics.Counts {
		counts[projectState(state)] = count
	}

	diagnoses := make(map[report.Diagnosis]int, len(metrics.Diagnoses))
	for diagnosis, count := range metrics.Diagnoses {
		diagnoses[projectDiagnosis(diagnosis)] = count
	}

	return report.Metrics{
		MutationScore:  metrics.MutationScore,
		AssertionScore: metrics.AssertionScore,
		Reachability:   metrics.Reachability,
		Incomplete:     metrics.Incomplete,
		Counts:         counts,
		Diagnoses:      diagnoses,
		Scored:         metrics.Scored,
	}
}

// projectOperatorErrors spells the per-operator summary onto the published
// report, order preserved.
func projectOperatorErrors(counts []oracle.OperatorErrors) []report.OperatorErrors {
	converted := make([]report.OperatorErrors, 0, len(counts))

	for _, entry := range counts {
		converted = append(converted, report.OperatorErrors{
			Operator:      string(entry.Operator),
			Generated:     entry.Generated,
			Invalid:       entry.Invalid,
			KilledByError: entry.KilledByError,
			ErrorRate:     entry.ErrorRate,
		})
	}

	return converted
}
