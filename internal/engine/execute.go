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
	config        config
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
	budget := timeoutBudget(plan.config, plan.prepared.baselineDuration)

	queue := make(chan int)
	mutex := sync.Mutex{}
	group := sync.WaitGroup{}

	workers := min(plan.config.Jobs, len(plan.described))

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
func timeoutBudget(config config, baseline time.Duration) time.Duration {
	scaled := time.Duration(float64(baseline) * config.TimeoutFactor)
	if scaled < config.TimeoutFloor {
		return config.TimeoutFloor
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
		Filters:       plan.config.TestSelection,
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

	verdict, failure := classify(ctx, plan, built, verdict, result, budget)
	if failure != nil || verdict.State != report.Survived {
		return verdict, failure
	}

	// Phase two: only a phase-one survivor is fingerprinted, which is the whole
	// point of the split.
	return executionOracle{plan: plan}.observe(ctx, built, index, verdict)
}

// classify assigns the aggregate state by the normative precedence, and runs
// terraform validate only where it is the sole discriminator: after an error.
// Every branch names its state through the oracle's terminal constructors; the
// outcome is projected onto the published mutant at the boundary.
func classify(
	ctx context.Context,
	plan executionPlan,
	built sandbox.Sandbox,
	verdict report.Mutant,
	result tfexec.TestResult,
	budget time.Duration,
) (report.Mutant, *report.ExecutionError) {
	// Killed is checked before Invalid even though the precedence table ranks
	// Invalid first. The two cannot both apply: a statically invalid
	// configuration never reaches an assertion, so it cannot produce a run that
	// reports fail. Checking here keeps the M11 speed win — the killed majority
	// never pays for validate — without changing any verdict.
	if result.HasStatus(tfexec.StatusFail) {
		return project(verdict, oracle.Killed()), nil
	}

	if result.HasStatus(tfexec.StatusError) || verdict.ExecutedRuns == 0 {
		validation, err := plan.runner.Validate(ctx, built.ModuleDir)
		if err != nil {
			return verdict, &report.ExecutionError{
				MutantID: verdict.ID,
				Site:     verdict.Site,
				Message:  "terraform validate could not be executed: " + err.Error(),
			}
		}

		verdict.Validated = true

		if !validation.Valid {
			verdict.Diagnostics = append(verdict.Diagnostics,
				diagnostics(validation.Diagnostics)...)

			return project(verdict, oracle.Invalid(oracleDiagnostics(verdict.Diagnostics))), nil
		}

		if result.HasStatus(tfexec.StatusError) {
			return project(verdict, oracle.KilledByError(oracleDiagnostics(verdict.Diagnostics))), nil
		}
	}

	if result.TimedOut {
		return project(verdict, oracle.TimedOut(budget)), nil
	}

	if verdict.ExecutedRuns == 0 {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message: "no run block executed, so no verdict is possible; " +
				"a filter that matches nothing exits zero and must never be read as survival",
		}
	}

	// Phase one is out of decisions: the mutant survived everything phase one
	// can see, and phase two decides what that means.
	return project(verdict, oracle.SurvivedPhaseOne()), nil
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
