package engine

import (
	"context"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// executionOracle gathers observations and performs the Terraform calls that
// the core oracle requests. Verdict selection belongs to oracle.Judge.
type executionOracle struct {
	plan executionPlan
}

// observeSurvivor judges a phase-one survivor from the verbose snapshot
// phase one already produced and projects the outcome returned by
// oracle.Judge.
//
// Phase one runs `-verbose`, so the mutant's own stream carries the plan
// payloads the fingerprint comparison reads — no second Terraform call is
// made. Only the volatility re-run invokes Terraform again.
func (o executionOracle) observeSurvivor(
	ctx context.Context,
	built sandbox.Sandbox,
	verdict report.Mutant,
	observation oracle.Observation,
	executed oracle.Outcome,
	telemetry *mutantCollector,
	result tfexec.TestResult,
) (report.Mutant, oracle.Outcome, *report.ExecutionError) {
	fingerprintPhase := telemetry.phase(phaseFingerprint)
	payloads, err := fingerprint.Canonicalise(result.Payloads)
	mask := o.plan.prepared.mask
	var delta fingerprint.Delta
	if err == nil {
		delta = fingerprint.Compare(mask, o.plan.prepared.payloads, payloads)
	}
	fingerprintPhase()

	if err != nil {
		return verdict, executed, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "the fingerprint run could not be evaluated: " + err.Error(),
		}
	}

	if refusal := missingRuns(verdict, delta); refusal != nil {
		return verdict, executed, refusal
	}

	observation = observation.WithPhaseTwo(
		mutation.Operator(verdict.Operator), delta, mask, fingerprint.Unknowns(payloads),
	)

	if observation.NeedsVolatilityRerun() {
		var rerunErr *report.ExecutionError
		observation, rerunErr = o.rerunForVolatility(ctx, built, verdict, payloads, observation,
			telemetry)
		if rerunErr != nil {
			return verdict, executed, rerunErr
		}
	}

	if observation.DeadlineExceeded() {
		verdict.DeadlineReached = true
	}

	oraclePhase := telemetry.phase(phaseOracle)
	outcome := oracle.Judge(observation)
	oraclePhase()

	return project(verdict, outcome), outcome, nil
}

// missingRuns is the operational refusal when a run block the baseline
// fingerprinted produced no payload under the mutant: phase one reported every
// run as passing, so a missing payload means the two phases disagree and
// neither can be trusted over the other.
func missingRuns(verdict report.Mutant, delta fingerprint.Delta) *report.ExecutionError {
	if len(delta.MissingRuns) == 0 {
		return nil
	}

	return &report.ExecutionError{
		MutantID: verdict.ID,
		Site:     verdict.Site,
		Message: "the fingerprint run produced no payload for " +
			strings.Join(delta.MissingRuns, ", ") + ", which phase one reported as passing",
	}
}

// rerunForVolatility runs the mutant once more and re-compares under the
// mutant's own volatility: the attributes that differed across its two runs.
// A re-run that outlives the budget records the deadline on the observation
// instead of a comparison: a decomposable volatility the run never delivered
// is judged as the overrun it is, not as an equality it did not prove.
func (o executionOracle) rerunForVolatility(
	ctx context.Context,
	built sandbox.Sandbox,
	verdict report.Mutant,
	payloads []fingerprint.Payload,
	observation oracle.Observation,
	telemetry *mutantCollector,
) (oracle.Observation, *report.ExecutionError) {
	rerunPhase := telemetry.phase(phaseVolatilityRerun)
	second, err := o.fingerprintRun(ctx, built)
	defer rerunPhase(telemetry.child("test", second.result.Usage, second.result.Started)...)
	if second.result.TimedOut {
		return observation.WithPhaseTwoDeadline(
			timeoutBudget(o.plan.settings, o.plan.prepared.baselineDuration),
		), nil
	}

	if err != nil {
		return observation, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "the volatility re-run could not be evaluated: " + err.Error(),
		}
	}

	mutantMask := fingerprint.Derive(payloads, second.payloads)
	mask := o.plan.prepared.mask.MergeMutantVolatility(mutantMask)

	return observation.WithSecondRun(
		fingerprint.Compare(mask, o.plan.prepared.payloads, payloads),
		mask,
		mutantMask.Paths(),
	), nil
}

// computedOf adapts the schema lookup to the oracle's computed question: a
// mock invents values for exactly the attributes the provider fills in, and
// whether the schema knew the resource type is the lookup's second
// result.
func computedOf(
	schemas tfexec.Schemas,
) func(kind, resourceType, attribute string) (computed, known bool) {
	return func(kind, resourceType, attribute string) (computed, known bool) {
		described, known := schemas.Attribute(kind, resourceType, []string{attribute})

		return described.Computed, known
	}
}

// observation assembles what the engine measured for the oracle to judge:
// the phase-one measurements — the runs' diagnostics already lifted into the
// context's shape, so the observation judges what the report will show — plus
// the fail-closed adapters over the graph cone, the output and local closure,
// the provider schemas and the mutant's own volatility scan. The engine keeps
// every I/O duty; the oracle judges.
func (o executionOracle) observation(
	index int,
	verdict report.Mutant,
	observed []oracle.Diagnostic,
	result tfexec.TestResult,
	budget time.Duration,
) oracle.Observation {
	return oracle.NewObservation(
		result.HasStatus(tfexec.StatusFail),
		result.HasStatus(tfexec.StatusError),
		verdict.ExecutedRuns,
		result.TimedOut,
		budget,
		observed,
	).
		WithCone(o.coneOf(verdict)).
		WithClosure(o.plan.closure.Reads).
		WithComputedAttributes(computedOf(o.plan.prepared.schemas)).
		WithSuspicion(o.suspicion(index))
}

// coneOf adapts the attribute-level reference graph to the payload-address
// rule the oracle judges unknowns by: an unmappable site keeps the
// whole-payload floor, where every unknown blocks.
func (o executionOracle) coneOf(verdict report.Mutant) func(string) bool {
	cone, ok := o.plan.graph.SiteCone(verdict.Module, verdict.Site)
	if !ok {
		return func(string) bool { return true }
	}

	return cone.ContainsPayloadAddress
}

// suspicion adapts the static impure scan over the mutant's own syntax. The
// scan runs once, on first use, exactly where the rerun rule needs it.
func (o executionOracle) suspicion(index int) func(string) bool {
	var scan discovery.VolatilityScan
	var ready bool

	return func(resource string) bool {
		if !ready {
			scan = o.mutantScan(index)
			ready = true
		}

		return scan.Suspicious(resource)
	}
}

// rerunSnapshot is the volatility re-run's projected content: the canonical
// payloads the second comparison reads and the raw result whose usage the
// telemetry records and whose timeout decides the deadline row.
type rerunSnapshot struct {
	payloads []fingerprint.Payload
	result   tfexec.TestResult
}

// fingerprintRun executes the volatility re-run — the only Terraform call a
// mutant can pay after its single verbose execution — and projects its
// payloads. The result is carried raw so the caller can record the child
// process's usage against the phase that invoked it.
func (o executionOracle) fingerprintRun(
	ctx context.Context,
	built sandbox.Sandbox,
) (rerunSnapshot, error) {
	result, err := o.plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: o.plan.configuration.TestDirRelative(),
		Filters:       o.plan.settings.TestSelection,
		Verbose:       true,
		Timeout:       timeoutBudget(o.plan.settings, o.plan.prepared.baselineDuration),
	})
	if err != nil {
		return rerunSnapshot{result: result}, err
	}

	payloads, err := fingerprint.Canonicalise(result.Payloads)
	if err != nil {
		return rerunSnapshot{result: result}, err
	}

	return rerunSnapshot{payloads: payloads, result: result}, nil
}

// phaseOne marks the run rows the mutant's single verbose execution produced.
// The two-phase engine also wrote phase-two rows — one per run block of the
// separate survivor-side fingerprint invocation; the single-phase engine
// writes none. The value survives in the published schema so a reader of an
// older report keeps a documented meaning for it.
const phaseOne = 1

// mutantScan runs the static impure scan over the mutant's own syntax.
func (o executionOracle) mutantScan(index int) discovery.VolatilityScan {
	mutant := o.plan.generated[index]

	sources := maps.Clone(o.plan.prepared.sources)
	sources[filepath.Join(o.plan.configuration.ClosureRoot, mutant.File)] = mutant.Mutated

	return o.plan.configuration.ScanVolatility(sources)
}
