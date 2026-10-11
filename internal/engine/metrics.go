package engine

import (
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The population arithmetic's projections.
//
// The classified population the oracle assigned in this run is the fact the
// arithmetic is computed from: it is constructed where each verdict was
// decided — the precedence table, the static shortcuts, the suppressions and
// the cache replay — and travels to the metrics as the Oracle context's own
// value, never as a re-parse of the published report. The result feeds the
// projections below, which spell it onto the published report; the published
// mutant is never an input to the arithmetic.

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
