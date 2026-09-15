package engine

import (
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The population arithmetic's input column.
//
// Every outcome the oracle assigned in this run was constructed at its
// classification site and projected onto the published mutant; the DTO carries
// the outcome's public record. complete() reads those records back through the
// oracle's constructors to give the metrics their []Outcome, so the arithmetic
// is computed over the Oracle context's own type. The reconstruction states
// exactly what each record proves — the recorded state, diagnosis, operator,
// diagnostics and suppression, and the published evidence behind them — and
// the result feeds the arithmetic alone; the published mutant is never
// rewritten from it.
//
// Two row classes carry a record no constructor in this run proved: a verdict
// replayed from the cache, and a mutant phase two could not run for. #103's
// parsing constructor replaces the replayed rows with a validated rebuild from
// the stored record.

// populationMetrics derives the report's metrics and per-operator error rates
// from the executed population's recorded outcomes.
func populationMetrics(
	executed []report.Mutant,
	budget time.Duration,
) (report.Metrics, []report.OperatorErrors) {
	outcomes := make([]oracle.Outcome, 0, len(executed))
	classified := make([]oracle.Classified, 0, len(executed))

	for _, mutant := range executed {
		outcome := recordedOutcome(mutant, budget)
		outcomes = append(outcomes, outcome)
		classified = append(classified, oracle.Classified{
			Operator: mutation.Operator(mutant.Operator),
			Outcome:  outcome,
		})
	}

	return projectMetrics(oracle.ComputeMetrics(outcomes)),
		projectOperatorErrors(oracle.ComputeOperatorErrors(classified))
}

// recordedOutcome reads one mutant's published record back through the
// constructor its state was decided by.
func recordedOutcome(mutant report.Mutant, budget time.Duration) oracle.Outcome {
	// The closed published state set is covered; the default is unreachable in
	// production and returns the state-only outcome for safety.
	switch mutant.State {
	case report.Invalid:
		return oracle.Invalid(oracleDiagnostics(mutant.Diagnostics))
	case report.Killed:
		return oracle.Killed()
	case report.KilledByError:
		return oracle.KilledByError(oracleDiagnostics(mutant.Diagnostics))
	case report.Timeout:
		return oracle.TimedOut(budget)
	case report.Survived:
		return recordedSurvivor(mutant)
	case report.StructurallyUnassertable:
		return oracle.StructurallyUnassertable(mutation.Operator(mutant.Operator))
	case report.Unobservable:
		return oracle.Unobservable(recordedMask(mutant.Verdict.Evidence))
	case report.NoCoverage:
		return oracle.NoCoverage(recordedNoCoverageClaim(mutant))
	case report.Ignored:
		return oracle.Ignored(oracleSuppression(mutant.Suppression))
	case report.Pending:
		return oracle.Pending()
	// A state outside the closed published set cannot be recorded; the
	// state-only outcome is the safe answer.
	default:
	}

	return oracle.Pending()
}

// recordedSurvivor reads a survivor's record back through the constructor its
// diagnosis was decided by. A survivor carrying no diagnosis is the provisional
// phase-one state, published only where phase two could not run at all.
func recordedSurvivor(mutant report.Mutant) oracle.Outcome {
	if mutant.Verdict == nil || mutant.Verdict.Diagnosis == "" {
		return oracle.SurvivedPhaseOne()
	}

	delta, mask := recordedEvidence(mutant.Verdict.Evidence)

	//nolint:exhaustive // mock-masked was withdrawn (issue #50) and is never emitted.
	switch mutant.Verdict.Diagnosis {
	case report.IndeterminateUnknownValues:
		return oracle.SurvivedIndeterminateUnknowns(mutant.Verdict.Evidence.UnknownPaths, mask)
	case report.IndeterminateVolatility:
		return oracle.SurvivedIndeterminateVolatility(
			delta, mask, mutant.Verdict.Evidence.UnstableAttributes,
		)
	case report.WeakAssertion:
		return oracle.SurvivedWeakAssertion(delta, mask, firstAddress(delta), readingReach(mutant.Verdict.Evidence))
	case report.NoAssertion:
		return oracle.SurvivedNoAssertion(delta, mask)
	case report.Unasserted:
		return oracle.SurvivedUnasserted(delta, mask, firstAddress(delta), defeatedReach(mutant.Verdict.Evidence))
	default:
		// A withdrawn diagnosis has no constructor: the record contributes its
		// state alone, and no diagnosis the oracle does not emit is counted.
		return oracle.SurvivedPhaseOne()
	}
}

// recordedEvidence rebuilds the delta and mask a survivor's published evidence
// carries. The published evidence is the projection the constructor recorded,
// so reading it back is exact.
func recordedEvidence(evidence report.Evidence) (fingerprint.Delta, fingerprint.Mask) {
	delta := fingerprint.Delta{}
	for _, change := range evidence.Delta {
		delta.Changes = append(delta.Changes, fingerprint.Change{
			Run:       change.Run,
			Path:      change.Path,
			Address:   change.Address,
			Baseline:  change.Baseline,
			Mutant:    change.Mutant,
			Sensitive: change.Sensitive,
		})
	}

	return delta, recordedMask(evidence)
}

// recordedMask rebuilds the mask whose paths the published evidence names.
func recordedMask(evidence report.Evidence) fingerprint.Mask {
	mask := fingerprint.NewMask()
	for _, path := range evidence.VolatileComponents {
		mask.Spans[path] = fingerprint.Span{}
	}

	return mask
}

// firstAddress names the delta address the closure verdict was read against.
// The recorded outcome's message quotes it; the published record keeps the
// message it was decided with, so this serves the arithmetic's column alone.
func firstAddress(delta fingerprint.Delta) string {
	if addresses := delta.Addresses(); len(addresses) > 0 {
		return addresses[0]
	}

	return ""
}

// readingReach rebuilds what the closure concluded about a read assertion.
func readingReach(evidence report.Evidence) discovery.Reach {
	return discovery.Reach{Read: true, Assertion: recordedAssertion(evidence.Assertion)}
}

// defeatedReach rebuilds what the closure concluded about a defeated read.
func defeatedReach(evidence report.Evidence) discovery.Reach {
	return discovery.Reach{
		Defeated:  true,
		Assertion: recordedAssertion(evidence.Assertion),
		Construct: evidence.DefeatedBy,
	}
}

// recordedAssertion splits a stored assertion location back into the test file
// and run the closure read. Run block names are HCL identifiers, so the first
// colon separates them.
func recordedAssertion(location string) discovery.Assertion {
	file, run, _ := strings.Cut(location, ":")

	return discovery.Assertion{File: file, Run: run}
}

// recordedNoCoverageClaim states the claim the record carries: a block-level
// pre-classification names its reason, and the module-level claim is the
// state alone.
func recordedNoCoverageClaim(mutant report.Mutant) string {
	if mutant.Verdict == nil {
		return ""
	}

	return mutant.Verdict.Message
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
