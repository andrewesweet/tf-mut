package engine

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

type executionPlan struct {
	runner        tfexec.Runner
	configuration discovery.Configuration
	settings      settings
	prepared      warmWorkspace
	generated     []mutation.Mutant
	described     []report.Mutant
	// populationDescribed is the whole post-suppression described population,
	// selection included, so a coverage warning is decided over what the
	// operators generated rather than over what the count levers kept.
	populationDescribed []report.Mutant
	// classified is the outcome classified for each described row, in row
	// order: the static shortcuts and suppressions classified theirs before
	// selection, and execution and the cache replay classify the rest.
	classified []oracle.Classified
	workRoot   string
	closure    discovery.Closure
	// graph is the attribute-level reference graph, built once per run from
	// the already-parsed ASTs (M3a.1).
	graph *discovery.Graph
	// collector is the run's performance telemetry; every mutant's record is
	// a child of it.
	collector *runCollector
	// events is the grading's dispatcher: each executed mutant's result and
	// the classified population are published to it (ADR-0005).
	events *dispatcher
}

// evaluatedPopulation is the per-mutant result's report projection: every
// row and its classified outcome in its own slot, in row order, and every
// operational failure.
type evaluatedPopulation struct {
	rows       []report.Mutant
	classified []oracle.Classified
	failures   []report.ExecutionError
	// trip is the consecutive-failure trip's record, present when its threshold
	// was reached, even if no mutants remained to dispatch. Both the trip warning
	// and the population provenance's unexecuted count come from this record.
	trip *tripOutcome
}

func (p *evaluatedPopulation) observe(event MutantEvaluated) error {
	p.rows[event.index] = event.row
	// Baseline labelling mutates report provenance after publication. Keep
	// that storage report-owned so the published event stays unchanged.
	if event.row.Provenance != nil {
		provenance := *event.row.Provenance
		p.rows[event.index].Provenance = &provenance
	}

	p.classified[event.index] = event.classified

	if event.failure != nil {
		p.failures = append(p.failures, *event.failure)
	}

	return nil
}

// execute evaluates pending mutants until exhaustion or a consecutive-failure
// trip, bounded by the configured job count, and publishes each dispatched result.
//
// Each evaluated mutant gets its own sandbox and result slot. Workers publish
// under one lock, so subscribers see one result at a time, in the order workers
// acquire the lock. The failure streak follows this publication order.
func execute(ctx context.Context, plan executionPlan) (evaluatedPopulation, error) {
	population := evaluatedPopulation{
		rows:       make([]report.Mutant, len(plan.described)),
		classified: make([]oracle.Classified, len(plan.classified)),
		failures:   make([]report.ExecutionError, 0),
	}
	copy(population.rows, plan.described)
	copy(population.classified, plan.classified)

	budget := timeoutBudget(plan.settings, plan.prepared.baselineDuration)

	mutex := sync.Mutex{}
	group := sync.WaitGroup{}
	next := 0

	var delivery error

	pending := 0
	for _, mutant := range plan.described {
		if mutant.State == report.Pending {
			pending++
		}
	}

	// The consecutive-failure trip: a dependency failing for every mutant is
	// reported after a few failures instead of after the whole population.
	trip := newTripCounter(plan.settings.FailureTripThreshold, pending)

	// The long-run line goes only where the caller pointed a writer; nowhere
	// is the default.
	progress := startProgress(ctx, plan, pending)
	defer progress.stopLine()

	// The report projection first, then the progress line.
	plan.events.mutantEvaluated.subscribe(population.observe)
	plan.events.mutantEvaluated.subscribe(progress.observe)

	workers := min(plan.settings.Jobs, len(plan.described))

	for range workers {
		group.Go(func() {
			for {
				mutex.Lock()
				for next < len(plan.described) && plan.described[next].State != report.Pending {
					next++
				}
				if trip.tripped || next == len(plan.described) {
					mutex.Unlock()

					return
				}
				index := next
				next++
				mutex.Unlock()

				verdict, outcome, failure := evaluate(ctx, plan, index, budget)

				mutex.Lock()
				trip.record(failure)
				err := plan.events.mutantEvaluated.publish(MutantEvaluated{
					index: index,
					row:   verdict,
					classified: oracle.Classified{
						Operator: plan.generated[index].Operator,
						Outcome:  outcome,
					},
					failure: failure,
				})
				if delivery == nil {
					delivery = err
				}
				mutex.Unlock()
			}
		})
	}

	group.Wait()

	population.trip = trip.outcome(population.rows, population.failures)
	plan.collector.trip = population.trip

	if delivery != nil {
		return evaluatedPopulation{}, delivery
	}

	slices.SortFunc(population.failures, func(left, right report.ExecutionError) int {
		return strings.Compare(left.MutantID, right.MutantID)
	})

	return population, nil
}

// tripOutcome records what the run observed when the failure threshold was
// reached: the streak that tripped it, the failure that started the streak, and the
// selected mutants never dispatched. It is execution's own fact — the report
// names the trip from it and the population provenance counts it.
type tripOutcome struct {
	consecutiveFailures int
	firstFailure        string
	unexecuted          int
}

// tripCounter counts consecutive operational failures across workers. Any
// verdict resets the count: the trip answers "is the dependency dead", and a
// mutant that produced a verdict proves it was alive for that mutant.
type tripCounter struct {
	// threshold is the consecutive-failure count the trip fires at, already
	// capped at the pending population so the trip stays reachable on a run
	// smaller than the default.
	threshold int

	consecutive  int
	firstFailure string
	tripped      bool
}

func newTripCounter(threshold, pending int) *tripCounter {
	if pending < threshold {
		threshold = pending
	}

	return &tripCounter{threshold: threshold}
}

// record folds one published result into the consecutive count: a verdict
// resets it, a failure extends it.
func (t *tripCounter) record(failure *report.ExecutionError) {
	if t.tripped {
		return
	}

	if failure == nil {
		t.consecutive = 0

		return
	}

	if t.consecutive == 0 {
		t.firstFailure = failure.Message
	}

	t.consecutive++
	t.tripped = t.consecutive >= t.threshold
}

// outcome is the trip's published record, or nil when the run never tripped.
// The unexecuted count is the pending rows minus the failures: every
// dispatched mutant either published a verdict or a failure, so what is left
// pending and unfailed is exactly what was never dispatched.
func (t *tripCounter) outcome(rows []report.Mutant, failures []report.ExecutionError) *tripOutcome {
	if !t.tripped {
		return nil
	}

	failed := make(map[string]struct{}, len(failures))
	for _, failure := range failures {
		failed[failure.MutantID] = struct{}{}
	}

	unexecuted := 0
	for index, row := range rows {
		if _, isFailed := failed[row.ID]; row.State == report.Pending && !isFailed {
			rows[index].Provenance.Execution = report.ExecutionUnexecuted
			unexecuted++
		}
	}

	return &tripOutcome{
		consecutiveFailures: t.threshold,
		firstFailure:        t.firstFailure,
		unexecuted:          unexecuted,
	}
}

// timeoutBudget is max(factor × baseline, floor), per the milestone spec.
func timeoutBudget(settings settings, baseline time.Duration) time.Duration {
	scaled := time.Duration(float64(baseline) * settings.TimeoutFactor)
	if scaled < settings.TimeoutFloor {
		return settings.TimeoutFloor
	}

	return scaled
}

// evaluate performs one mutant's evaluation. The mutant's telemetry is
// always recorded, and the report's execution errors are events on the
// mutant's span — registered after the finish bracket, so they run first,
// while the span still records — so a trace carries the same failure the
// report does. A panic in the evaluation — a bug, not a verdict — is
// converted into that mutant's operational failure; a worker that dies must
// not take the rest of the population with it.
func evaluate(
	ctx context.Context,
	plan executionPlan,
	index int,
	budget time.Duration,
) (verdict report.Mutant, outcome oracle.Outcome, failure *report.ExecutionError) {
	verdict = plan.described[index]

	// The mutant's own record: always on, published beside the numbers, and
	// carried onto the published mutant even when the evaluation fails partway.
	telemetry := plan.collector.mutant(ctx, verdict.ID, verdict.Operator)
	defer func() { verdict.Telemetry = telemetry.finish() }()
	defer func() {
		if recovered := recover(); recovered != nil {
			verdict = plan.described[index]
			outcome = plan.classified[index].Outcome
			failure = &report.ExecutionError{
				MutantID: verdict.ID,
				Site:     verdict.Site,
				Message:  fmt.Sprintf("evaluation panicked: %v", recovered),
			}
		}

		if failure != nil {
			telemetry.recordError(failure.Message)
		}
	}()

	return evaluateOne(ctx, plan, index, budget, verdict, telemetry)
}

// evaluateOne runs the mutant's evaluation itself: stage, mutate, execute,
// judge. The single invocation runs verbose, so a survivor's fingerprint
// comparison reads the stream it produced.
func evaluateOne(
	ctx context.Context,
	plan executionPlan,
	index int,
	budget time.Duration,
	verdict report.Mutant,
	telemetry *mutantCollector,
) (report.Mutant, oracle.Outcome, *report.ExecutionError) {
	built, err := materialiseMutant(plan, index, telemetry)
	if err != nil {
		return verdict, plan.classified[index].Outcome, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "sandbox could not be materialised: " + err.Error(),
		}
	}

	defer func() { _ = os.RemoveAll(built.Root) }()

	testPhase := telemetry.phase(phaseTest)
	result, err := plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: plan.configuration.TestDirRelative(),
		Filters:       plan.settings.TestSelection,
		Verbose:       true,
		Timeout:       budget,
	})
	testPhase(telemetry.child("test", result.Usage, result.Started)...)
	if err != nil {
		return verdict, plan.classified[index].Outcome, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "terraform could not be executed: " + err.Error(),
		}
	}

	verdict.Runs = runOutcomes(result)
	verdict.ExecutedRuns = result.ExecutedRuns()
	// The runs' diagnostics are lifted once, into the Oracle context's shape.
	// The published diagnostics are the projection of that value, so the
	// observation judges the same set the report shows.
	observed := diagnostics(result.Diagnostics)
	verdict.Diagnostics = projectDiagnostics(observed)
	// The deadline is an evaluation fact, not a verdict: when the invocation
	// was cut short the finished runs still stand, and whatever verdict they
	// earned keeps its precedence — the deadline is recorded beside it so the
	// report shows both.
	verdict.DeadlineReached = result.TimedOut

	// D18: a stream that stopped early is not a survival. A non-zero exit
	// with no failing or erroring run reuses the baseline's own exit-status
	// rule, and fewer executed runs than the baseline fingerprinted refuses
	// even at exit zero. Both are execution errors, never Killed or Survived.
	if message, refused := phaseOneTruncation(
		result, plan.prepared.baselineRuns, len(plan.settings.TestSelection) > 0,
	); refused {
		return verdict, plan.classified[index].Outcome, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  message,
		}
	}

	judge := executionOracle{plan: plan}
	observation := judge.observation(index, verdict, observed, result, budget)

	if observation.NeedsValidate() {
		var failure *report.ExecutionError
		verdict, observation, failure = judge.validate(ctx, built, verdict, observed, observation, telemetry)
		if failure != nil {
			return verdict, plan.classified[index].Outcome, failure
		}
	}

	outcome := oracle.Judge(observation)

	// A phase-one survivor over zero executed runs is the operational refusal:
	// no verdict is possible, and the mutant is never projected as a survival.
	if observation.NoVerdictPossible() && observation.NeedsPhaseTwo(outcome) {
		return verdict, plan.classified[index].Outcome, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message: "no run block executed, so no verdict is possible; " +
				"a filter that matches nothing exits zero and must never be read as survival",
		}
	}

	verdict = project(verdict, outcome)

	// A phase-one survivor is fingerprinted from the stream phase one already
	// produced — the invocation ran verbose, so no second Terraform call is
	// made — and only a volatility re-run invokes Terraform again.
	if !observation.NeedsPhaseTwo(outcome) {
		return verdict, outcome, nil
	}

	return judge.observeSurvivor(ctx, built, verdict, observation, outcome, telemetry, result)
}

func materialiseMutant(plan executionPlan, index int, telemetry *mutantCollector) (sandbox.Sandbox, error) {
	mutant := plan.generated[index]
	target := filepath.Join(plan.workRoot, fmt.Sprintf("m%04d-%s", index, mutant.ID))

	sandboxPhase := telemetry.phase(phaseSandbox)
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: plan.configuration.ClosureRoot,
		ModuleRel:  plan.configuration.RootRelative(),
		Target:     target,
		Mutations:  map[string][]byte{mutant.File: mutant.Mutated},
		Share: &sandbox.Share{
			DataDir:      plan.prepared.dataDir,
			LockFile:     plan.prepared.lockFile,
			HashFreeLock: true,
			Guard:        plan.prepared.providerGuard,
		},
		Hardlink: true,
	})
	sandboxPhase()

	return built, err
}

// phaseOneTruncation refuses a phase-one stream that stopped early: a non-zero
// Terraform exit with no failing or erroring run, or fewer executed runs than
// the baseline fingerprinted. Either proves nothing about the mutant, so the
// refusal is an execution error, never Killed or Survived.
//
// The exit-status rule is the baseline's own (baseline.go): the baseline
// refuses a non-zero exit with no failing or erroring run as red, and phase
// one does the same per mutant. The count rule catches the truncation an exit
// of zero would otherwise miss. A failing or erroring run keeps its verdict —
// the rules target false survivals, not proven kills — and a timeout keeps
// its deadline handling. Zero runs stay with the existing zero-run refusal,
// and a filtered selection legitimately runs fewer than the unfiltered
// baseline, so the count rule applies only unfiltered. A phase-one exit 11 —
// a Terraform crash, not a test verdict — lands here as an execution error
// through the non-zero exit arm.
func phaseOneTruncation(result tfexec.TestResult, baselineRuns int, filtered bool) (string, bool) {
	if result.TimedOut {
		return "", false
	}

	if result.ExecutedRuns() == 0 {
		return "", false
	}

	if result.HasStatus(tfexec.StatusFail) || result.HasStatus(tfexec.StatusError) {
		return "", false
	}

	if result.ExitCode != 0 {
		return fmt.Sprintf(
			"terraform test exited %d after %d run block(s) with no failing or erroring run, "+
				"so no verdict is possible",
			result.ExitCode, result.ExecutedRuns(),
		), true
	}

	if !filtered && baselineRuns > 0 && result.ExecutedRuns() < baselineRuns {
		return fmt.Sprintf(
			"only %d of %d baseline run block(s) executed with no failing or erroring run, "+
				"so no verdict is possible",
			result.ExecutedRuns(), baselineRuns,
		), true
	}

	return "", false
}

// validate records the terraform validate verdict into the observation where
// the precedence table needs it: validate is the sole discriminator between
// Invalid and KilledByError after a run-level error, and the merged, ordered
// diagnostics are the evidence an Invalid claim is made from. The merge
// happens in the Oracle context's shape — the runs' diagnostics already
// lifted, validate's lifted beside them — and the published mutant carries
// its projection.
func (o executionOracle) validate(
	ctx context.Context,
	built sandbox.Sandbox,
	verdict report.Mutant,
	observed []oracle.Diagnostic,
	observation oracle.Observation,
	telemetry *mutantCollector,
) (report.Mutant, oracle.Observation, *report.ExecutionError) {
	validatePhase := telemetry.phase(phaseValidate)
	// validate is bounded by the absolute preflight cap, not the mutant's
	// execution budget: it is the precedence table's sole discriminator
	// between Invalid and KilledByError, so a budget that starves it would
	// leave the table without its evidence. A hung validate is a hung binary,
	// not a slow mutant, and the absolute cap is the honest bound.
	validation, err := o.plan.runner.Validate(ctx, built.ModuleDir, o.plan.settings.preflightCap())
	defer validatePhase(telemetry.child("validate", validation.Usage, validation.Started)...)
	if err != nil {
		return verdict, observation, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "terraform validate could not be executed: " + err.Error(),
		}
	}

	verdict.Validated = true

	if validation.Valid {
		return verdict, observation.WithValidateVerdict(true, nil), nil
	}

	merged := append(slices.Clone(observed), diagnostics(validation.Diagnostics)...)
	slices.SortFunc(merged, compareDiagnostics)
	verdict.Diagnostics = projectDiagnostics(merged)

	return verdict, observation.WithValidateVerdict(false, merged), nil
}

func runOutcomes(result tfexec.TestResult) []report.RunOutcome {
	outcomes := make([]report.RunOutcome, 0, len(result.Runs))

	for _, run := range result.Runs {
		outcomes = append(outcomes, report.RunOutcome{
			File:      run.File,
			Run:       run.Run,
			Phase:     phaseOne,
			Status:    run.Status,
			ElapsedMS: run.ElapsedMS,
		})
	}

	return outcomes
}

// diagnostics lifts the runs' diagnostics into the Oracle context's shape,
// in the canonical order.
func diagnostics(source []tfexec.Diagnostic) []oracle.Diagnostic {
	converted := make([]oracle.Diagnostic, 0, len(source))

	for _, diagnostic := range source {
		entry := oracle.Diagnostic{
			Severity: diagnostic.Severity,
			Summary:  diagnostic.Summary,
			Detail:   diagnostic.Detail,
			Range:    nil,
			TestFile: diagnostic.TestFile,
			TestRun:  diagnostic.TestRun,
		}

		if diagnostic.Range != nil {
			entry.Range = &oracle.Range{
				File:  filepath.ToSlash(diagnostic.Range.Filename),
				Start: oracle.Position{Line: diagnostic.Range.Start.Line, Column: diagnostic.Range.Start.Column},
				End:   oracle.Position{Line: diagnostic.Range.End.Line, Column: diagnostic.Range.End.Column},
			}
		}

		converted = append(converted, entry)
	}

	// Terraform's parallel evaluation emits one plan's errors in a
	// nondeterministic order, and a report that varied run to run in everything
	// but that order would break the before-and-after proofs the baseline
	// honesty rests on. The recorded list is the same set either way; the
	// canonical order is what makes the set observable.
	slices.SortFunc(converted, compareDiagnostics)

	return converted
}

func compareDiagnostics(left, right oracle.Diagnostic) int {
	return cmp.Or(
		cmp.Compare(left.TestFile, right.TestFile),
		cmp.Compare(left.TestRun, right.TestRun),
		cmp.Compare(left.Severity, right.Severity),
		cmp.Compare(left.Summary, right.Summary),
		cmp.Compare(left.Detail, right.Detail),
		compareDiagnosticRanges(left.Range, right.Range),
	)
}

func compareDiagnosticRanges(left, right *oracle.Range) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return 1
	case right == nil:
		return -1
	}

	return cmp.Or(
		cmp.Compare(left.File, right.File),
		cmp.Compare(left.Start.Line, right.Start.Line),
		cmp.Compare(left.Start.Column, right.Start.Column),
		cmp.Compare(left.End.Line, right.End.Line),
		cmp.Compare(left.End.Column, right.End.Column),
	)
}
