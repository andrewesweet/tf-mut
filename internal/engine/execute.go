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
	prepared      warm
	generated     []mutation.Mutant
	described     []report.Mutant
	workRoot      string
	closure       discovery.Closure
	// graph is the attribute-level reference graph, built once per run from
	// the already-parsed ASTs (M3a.1).
	graph *discovery.Graph
}

// execute runs every executable mutant, bounded by the configured job count.
//
// Verdicts do not depend on the parallelism level: each mutant gets its own
// sandbox, results are written back to their own slot, and a failure in one
// worker cannot reach another.
func execute(ctx context.Context, plan executionPlan) ([]report.Mutant, []report.ExecutionError) {
	results := make([]report.Mutant, len(plan.described))
	copy(results, plan.described)

	failures := make([]report.ExecutionError, 0)
	budget := timeoutBudget(plan.settings, plan.prepared.baselineDuration)

	queue := make(chan int)
	mutex := sync.Mutex{}
	group := sync.WaitGroup{}

	workers := min(plan.settings.Jobs, len(plan.described))

	for range workers {
		group.Go(func() {
			for index := range queue {
				verdict, failure := evaluateSafely(ctx, plan, index, budget)

				mutex.Lock()
				results[index] = verdict

				if failure != nil {
					failures = append(failures, *failure)
				}
				mutex.Unlock()
			}
		})
	}

	for index, mutant := range plan.described {
		if mutant.State != report.Pending {
			// Statically classified, suppressed, or replayed from the cache:
			// nothing left to execute.
			continue
		}

		queue <- index
	}

	close(queue)
	group.Wait()

	slices.SortFunc(failures, func(left, right report.ExecutionError) int {
		return strings.Compare(left.MutantID, right.MutantID)
	})

	return results, failures
}

// timeoutBudget is max(factor × baseline, floor), per the milestone spec.
func timeoutBudget(settings settings, baseline time.Duration) time.Duration {
	scaled := time.Duration(float64(baseline) * settings.TimeoutFactor)
	if scaled < settings.TimeoutFloor {
		return settings.TimeoutFloor
	}

	return scaled
}

// evaluateSafely converts a panic in one mutant's evaluation into that
// mutant's operational failure. A worker that dies must not take the rest of
// the population with it.
func evaluateSafely(
	ctx context.Context,
	plan executionPlan,
	index int,
	budget time.Duration,
) (verdict report.Mutant, failure *report.ExecutionError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			verdict = plan.described[index]
			failure = &report.ExecutionError{
				MutantID: verdict.ID,
				Site:     verdict.Site,
				Message:  fmt.Sprintf("evaluation panicked: %v", recovered),
			}
		}
	}()

	return evaluate(ctx, plan, index, budget)
}

func evaluate(
	ctx context.Context,
	plan executionPlan,
	index int,
	budget time.Duration,
) (report.Mutant, *report.ExecutionError) {
	verdict := plan.described[index]
	mutant := plan.generated[index]

	target := filepath.Join(plan.workRoot, fmt.Sprintf("m%04d-%s", index, mutant.ID))

	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: plan.configuration.ClosureRoot,
		ModuleRel:  plan.configuration.RootRelative(),
		Target:     target,
		Mutations:  map[string][]byte{mutant.File: mutant.Mutated},
		Share: &sandbox.Share{
			DataDir:  plan.prepared.dataDir,
			LockFile: plan.prepared.lockFile,
		},
		Hardlink: true,
	})
	if err != nil {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "sandbox could not be materialised: " + err.Error(),
		}
	}

	defer func() { _ = os.RemoveAll(built.Root) }()

	result, err := plan.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: plan.configuration.TestDirRelative(),
		Filters:       plan.settings.TestSelection,
		Timeout:       budget,
	})
	if err != nil {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message:  "terraform could not be executed: " + err.Error(),
		}
	}

	verdict.Runs = runOutcomes(result)
	verdict.ExecutedRuns = result.ExecutedRuns()
	verdict.Diagnostics = diagnostics(result.Diagnostics)

	judge := executionOracle{plan: plan}
	observation := judge.observation(index, verdict, result, budget)

	if observation.NeedsValidate() {
		var failure *report.ExecutionError
		verdict, observation, failure = judge.validate(ctx, built, verdict, observation)
		if failure != nil {
			return verdict, failure
		}
	}

	outcome := oracle.Judge(observation)

	// A phase-one survivor over zero executed runs is the operational refusal:
	// no verdict is possible, and the mutant is never projected as a survival.
	if observation.NoVerdictPossible() && observation.NeedsPhaseTwo(outcome) {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message: "no run block executed, so no verdict is possible; " +
				"a filter that matches nothing exits zero and must never be read as survival",
		}
	}

	verdict = project(verdict, outcome)

	// Phase two: only a phase-one survivor is fingerprinted, which is the whole
	// point of the split.
	if !observation.NeedsPhaseTwo(outcome) {
		return verdict, nil
	}

	return judge.observe(ctx, built, verdict, observation)
}

// validate records the terraform validate verdict into the observation where
// the precedence table needs it: validate is the sole discriminator between
// Invalid and KilledByError after a run-level error, and the merged, ordered
// diagnostics are the evidence an Invalid claim is made from.
func (o executionOracle) validate(
	ctx context.Context,
	built sandbox.Sandbox,
	verdict report.Mutant,
	observation oracle.Observation,
) (report.Mutant, oracle.Observation, *report.ExecutionError) {
	validation, err := o.plan.runner.Validate(ctx, built.ModuleDir)
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

	verdict.Diagnostics = append(verdict.Diagnostics, diagnostics(validation.Diagnostics)...)
	slices.SortFunc(verdict.Diagnostics, compareDiagnostics)

	return verdict,
		observation.WithValidateVerdict(false, oracleDiagnostics(verdict.Diagnostics)), nil
}

func runOutcomes(result tfexec.TestResult) []report.RunOutcome {
	outcomes := make([]report.RunOutcome, 0, len(result.Runs))

	for _, run := range result.Runs {
		outcomes = append(outcomes, report.RunOutcome{
			File:   run.File,
			Run:    run.Run,
			Phase:  phaseOne,
			Status: run.Status,
		})
	}

	return outcomes
}

func diagnostics(source []tfexec.Diagnostic) []report.Diagnostic {
	converted := make([]report.Diagnostic, 0, len(source))

	for _, diagnostic := range source {
		entry := report.Diagnostic{
			Severity: diagnostic.Severity,
			Summary:  diagnostic.Summary,
			Detail:   diagnostic.Detail,
			Range:    nil,
			TestFile: diagnostic.TestFile,
			TestRun:  diagnostic.TestRun,
		}

		if diagnostic.Range != nil {
			entry.Range = &report.Range{
				File:  filepath.ToSlash(diagnostic.Range.Filename),
				Start: report.Position{Line: diagnostic.Range.Start.Line, Column: diagnostic.Range.Start.Column},
				End:   report.Position{Line: diagnostic.Range.End.Line, Column: diagnostic.Range.End.Column},
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

func compareDiagnostics(left, right report.Diagnostic) int {
	return cmp.Or(
		cmp.Compare(left.TestFile, right.TestFile),
		cmp.Compare(left.TestRun, right.TestRun),
		cmp.Compare(left.Severity, right.Severity),
		cmp.Compare(left.Summary, right.Summary),
		cmp.Compare(left.Detail, right.Detail),
		compareDiagnosticRanges(left.Range, right.Range),
	)
}

func compareDiagnosticRanges(left, right *report.Range) int {
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
