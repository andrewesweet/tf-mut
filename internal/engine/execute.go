package engine

import (
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
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

type executionPlan struct {
	runner        tfexec.Runner
	configuration discovery.Configuration
	config        Config
	prepared      warm
	generated     []mutation.Mutant
	described     []report.Mutant
	workRoot      string
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
		if mutant.State == report.NoCoverage {
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
func timeoutBudget(config Config, baseline time.Duration) time.Duration {
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

	return classify(ctx, plan, built, verdict, result)
}

// classify assigns the aggregate state by the normative precedence, and runs
// terraform validate only where it is the sole discriminator: after an error.
func classify(
	ctx context.Context,
	plan executionPlan,
	built sandbox.Sandbox,
	verdict report.Mutant,
	result tfexec.TestResult,
) (report.Mutant, *report.ExecutionError) {
	// Killed is checked before Invalid even though the precedence table ranks
	// Invalid first. The two cannot both apply: a statically invalid
	// configuration never reaches an assertion, so it cannot produce a run that
	// reports fail. Checking here keeps the M11 speed win — the killed majority
	// never pays for validate — without changing any verdict.
	if result.HasStatus(tfexec.StatusFail) {
		verdict.State = report.Killed

		return verdict, nil
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
			verdict.State = report.Invalid
			verdict.Diagnostics = append(verdict.Diagnostics,
				diagnostics(validation.Diagnostics)...)

			return verdict, nil
		}

		if result.HasStatus(tfexec.StatusError) {
			verdict.State = report.KilledByError

			return verdict, nil
		}
	}

	if result.TimedOut {
		verdict.State = report.Timeout

		return verdict, nil
	}

	if verdict.ExecutedRuns == 0 {
		return verdict, &report.ExecutionError{
			MutantID: verdict.ID,
			Site:     verdict.Site,
			Message: "no run block executed, so no verdict is possible; " +
				"a filter that matches nothing exits zero and must never be read as survival",
		}
	}

	verdict.State = report.Survived

	return verdict, nil
}

func runOutcomes(result tfexec.TestResult) []report.RunOutcome {
	outcomes := make([]report.RunOutcome, 0, len(result.Runs))

	for _, run := range result.Runs {
		outcomes = append(outcomes, report.RunOutcome{
			File:   run.File,
			Run:    run.Run,
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

	return converted
}
