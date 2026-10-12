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

// observe gathers phase-two evidence for one phase-one survivor and projects
// the outcome returned by oracle.Judge.
//
// Phase two exists because `-verbose` embeds the whole provider schema in every
// per-run message — 20,288 times the output volume, measured — so only the
// non-killed minority may pay for it.
func (o executionOracle) observe(
	ctx context.Context,
	built sandbox.Sandbox,
	verdict report.Mutant,
	observation oracle.Observation,
	phaseOne oracle.Outcome,
	telemetry *mutantCollector,
) (report.Mutant, oracle.Outcome, *report.ExecutionError) {
	fingerprintPhase := telemetry.phase(phaseFingerprint)
	run, err := o.fingerprintRun(ctx, built)
	fingerprintPhase(telemetry.child("test", run.result.Usage, run.result.Started)...)
	verdict.Runs = append(verdict.Runs, run.outcomes...)

	budget := timeoutBudget(o.plan.settings, o.plan.prepared.baselineDuration)

	if run.result.TimedOut {
		verdict.DeadlineReached = true
		oraclePhase := telemetry.phase(phaseOracle)
		outcome := oracle.Judge(observation.WithPhaseTwoDeadline(budget))
		oraclePhase()

		return project(verdict, outcome), outcome, nil
	}

	if err != nil {
		return verdict, phaseOne, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "the fingerprint run could not be evaluated: " + err.Error(),
		}
	}

	mask := o.plan.prepared.mask
	delta := fingerprint.Compare(mask, o.plan.prepared.payloads, run.payloads)

	if refusal := missingRuns(verdict, delta); refusal != nil {
		return verdict, phaseOne, refusal
	}

	observation = observation.WithPhaseTwo(
		mutation.Operator(verdict.Operator), delta, mask, fingerprint.Unknowns(run.payloads),
	)

	if observation.NeedsVolatilityRerun() {
		var rerunErr *report.ExecutionError
		observation, rerunErr = o.rerunForVolatility(ctx, built, verdict, run.payloads, observation,
			telemetry)
		if rerunErr != nil {
			return verdict, phaseOne, rerunErr
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
// whether the schema knew the attribute at all is the lookup's second
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
// the phase-one measurements, plus the fail-closed adapters over the graph
// cone, the output and local closure, the provider schemas and the mutant's
// own volatility scan. The engine keeps every I/O duty; the oracle judges.
func (o executionOracle) observation(
	index int,
	verdict report.Mutant,
	result tfexec.TestResult,
	budget time.Duration,
) oracle.Observation {
	return oracle.NewObservation(
		result.HasStatus(tfexec.StatusFail),
		result.HasStatus(tfexec.StatusError),
		verdict.ExecutedRuns,
		result.TimedOut,
		budget,
		oracleDiagnostics(verdict.Diagnostics),
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

// fingerprintSnapshot is one verbose invocation's projected content: the
// canonical payloads the oracle compares, the per-run-block outcomes the
// verdict records, and the raw result whose usage the telemetry records.
type fingerprintSnapshot struct {
	payloads []fingerprint.Payload
	outcomes []report.RunOutcome
	result   tfexec.TestResult
}

// fingerprintRun executes the verbose phase and projects its payloads. The
// snapshot's result is the raw invocation, carried so the caller can record
// the child process's usage against the phase that invoked it.
func (o executionOracle) fingerprintRun(
	ctx context.Context,
	built sandbox.Sandbox,
) (fingerprintSnapshot, error) {
	result, err := o.plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: o.plan.configuration.TestDirRelative(),
		Filters:       o.plan.settings.TestSelection,
		Verbose:       true,
		Timeout:       timeoutBudget(o.plan.settings, o.plan.prepared.baselineDuration),
	})
	if err != nil {
		return fingerprintSnapshot{result: result}, err
	}

	payloads, err := fingerprint.Canonicalise(result.Payloads)
	if err != nil {
		return fingerprintSnapshot{result: result}, err
	}

	outcomes := make([]report.RunOutcome, 0, len(result.Runs))
	for _, run := range result.Runs {
		outcomes = append(outcomes, report.RunOutcome{
			File: run.File, Run: run.Run, Phase: phaseTwo, Status: run.Status,
		})
	}

	return fingerprintSnapshot{payloads: payloads, outcomes: outcomes, result: result}, nil
}

// The two execution phases, recorded per run so that a reader can confirm the
// verbose phase ran only where the design says it should.
const (
	phaseOne = 1
	phaseTwo = 2
)

// mutantScan runs the static impure scan over the mutant's own syntax.
func (o executionOracle) mutantScan(index int) discovery.VolatilityScan {
	mutant := o.plan.generated[index]

	sources := maps.Clone(o.plan.prepared.sources)
	sources[filepath.Join(o.plan.configuration.ClosureRoot, mutant.File)] = mutant.Mutated

	return o.plan.configuration.ScanVolatility(sources)
}
