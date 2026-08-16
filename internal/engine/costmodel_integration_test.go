//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The M2 pre-work measurement (issue #19). M1 measured the per-mutant cost
// against a real provider schema and found it dominated by something none of
// M2's planned levers address. These three experiments establish the cost model
// those levers will be judged against, before either is designed.
//
// Everything here drives the same execution path the engine uses. No product
// code was added to answer these questions.

const (
	// costModelRepetitions is how many times each configuration is timed. The
	// published figure is the fastest, which is the least contaminated by other
	// load on the machine; the spread is published alongside it.
	costModelRepetitions = 3
	// costModelBlockCounts are the run-block counts experiment one sweeps.
	costModelBlockCountOne   = 1
	costModelBlockCountTwo   = 2
	costModelBlockCountFour  = 4
	costModelBlockCountEight = 8
)

// timing is one configuration's measured wall times.
type timing struct {
	// RunBlocks is how many run blocks the configuration executed in total.
	RunBlocks int `json:"run_blocks"`
	// Invocations is how many terraform test processes it took to do that.
	Invocations int `json:"invocations"`
	// FastestSeconds is the quickest of the repetitions.
	FastestSeconds float64 `json:"fastest_seconds"`
	// SlowestSeconds is the slowest, published so the spread is visible.
	SlowestSeconds float64 `json:"slowest_seconds"`
}

// costModel is the published shape of all three experiments.
type costModel struct {
	Fixture          string   `json:"fixture"`
	TerraformVersion string   `json:"terraform_version"`
	Cores            int      `json:"cores"`
	Unsplit          []timing `json:"unsplit"`
	Split            []timing `json:"split"`
	FixedSeconds     float64  `json:"fixed_cost_seconds"`
	MarginalSeconds  float64  `json:"marginal_cost_per_run_block_seconds"`
	Verbose          []timing `json:"verbose"`
	Plain            []timing `json:"plain"`
	VerboseBytes     int      `json:"verbose_stdout_bytes"`
	PlainBytes       int      `json:"plain_stdout_bytes"`
	SelectionYield   []yield  `json:"selection_yield"`
}

// yield is one fixture's instantiation-reachability selection result.
type yield struct {
	Fixture string `json:"fixture"`
	// Mutants is the number of mutation sites considered.
	Mutants int `json:"mutants"`
	// TotalRunBlocks is the suite's run-block count.
	TotalRunBlocks int `json:"total_run_blocks"`
	// SelectedRunBlocks is the total run blocks selection would execute across
	// every mutant; the unselected engine executes Mutants x TotalRunBlocks.
	SelectedRunBlocks int `json:"selected_run_blocks"`
	// RemovedShare is the fraction of run-block executions selection removes.
	RemovedShare float64 `json:"removed_share"`
}

// It does not run in parallel: it is a wall-clock measurement, and a
// concurrent test would be measuring the other test.
//
//nolint:paralleltest // a timing measurement cannot share the machine.
func TestCostModelForTheM2SpeedLevers(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)
	config := networkConfig(t, module)
	runner := tfexec.Runner{Binary: "terraform", Env: config.Env}

	warm := prepareCostModelWorkspace(t, runner, module, config.WorkDir)

	measured := costModel{
		Fixture:          awsMockedFixture,
		TerraformVersion: "1.15.8",
		Cores:            runtime.NumCPU(),
	}

	measureInvocationCost(t, runner, warm, &measured)
	measureVerboseCost(t, runner, warm, &measured)
	measured.SelectionYield = measureSelectionYield(t)

	publishCostModel(t, measured)
}

// measureInvocationCost is experiment one: does splitting a suite into
// one-run-per-file add or remove work?
func measureInvocationCost(t *testing.T, runner tfexec.Runner, warm string, measured *costModel) {
	t.Helper()

	counts := []int{
		costModelBlockCountOne,
		costModelBlockCountTwo,
		costModelBlockCountFour,
		costModelBlockCountEight,
	}

	// The first sweep of this fixture showed wall time *falling* as run blocks
	// were added, which is a warm-up artefact of measuring in ascending order,
	// not a negative marginal cost. Each configuration is therefore measured in
	// both directions and each timing is preceded by an untimed warm-up run.
	unsplit := map[int]timing{}
	split := map[int]timing{}

	for _, order := range [][]int{counts, reversed(counts)} {
		for _, count := range order {
			writeSuite(t, warm, count, false)
			record(unsplit, count, timeSuite(t, runner, warm, count, 1, nil))

			writeSuite(t, warm, count, true)

			filters := make([]string, 0, count)
			for index := range count {
				filters = append(filters, fmt.Sprintf("tests/run%02d.tftest.hcl", index))
			}

			record(split, count, timeSuite(t, runner, warm, count, count, filters))
		}
	}

	for _, count := range counts {
		measured.Unsplit = append(measured.Unsplit, unsplit[count])
		measured.Split = append(measured.Split, split[count])
	}

	measured.FixedSeconds, measured.MarginalSeconds = fitCostModel(measured.Unsplit)

	t.Logf("fixed cost %.2fs per invocation, marginal cost %.2fs per run block",
		measured.FixedSeconds, measured.MarginalSeconds)
}

// timeSuite runs a configuration costModelRepetitions times and records the
// fastest and slowest. When filters are supplied each one is a separate
// invocation, which is what splitting produces.
func timeSuite(t *testing.T, runner tfexec.Runner, warm string, blocks, invocations int, filters []string) timing {
	t.Helper()

	// An untimed run first: the page cache and the provider plugin must already
	// be warm, or the first measurement of any configuration pays for warming
	// them and the sweep measures its own ordering.
	warmUp(t, runner, warm, filters)

	fastest := time.Duration(0)
	slowest := time.Duration(0)

	for repetition := range costModelRepetitions {
		started := time.Now()

		if len(filters) == 0 {
			runSuite(t, runner, warm, nil)
		} else {
			for _, filter := range filters {
				runSuite(t, runner, warm, []string{filter})
			}
		}

		elapsed := time.Since(started)
		if repetition == 0 || elapsed < fastest {
			fastest = elapsed
		}

		if elapsed > slowest {
			slowest = elapsed
		}
	}

	return timing{
		RunBlocks:      blocks,
		Invocations:    invocations,
		FastestSeconds: fastest.Seconds(),
		SlowestSeconds: slowest.Seconds(),
	}
}

// warmUp executes the configuration once without timing it.
func warmUp(t *testing.T, runner tfexec.Runner, warm string, filters []string) {
	t.Helper()

	if len(filters) == 0 {
		runSuite(t, runner, warm, nil)

		return
	}

	for _, filter := range filters {
		runSuite(t, runner, warm, []string{filter})
	}
}

// record keeps the fastest observation of a configuration across sweeps.
func record(into map[int]timing, count int, observed timing) {
	existing, seen := into[count]
	if !seen || observed.FastestSeconds < existing.FastestSeconds {
		if seen && existing.SlowestSeconds > observed.SlowestSeconds {
			observed.SlowestSeconds = existing.SlowestSeconds
		}

		into[count] = observed

		return
	}

	if observed.SlowestSeconds > existing.SlowestSeconds {
		existing.SlowestSeconds = observed.SlowestSeconds
		into[count] = existing
	}
}

func reversed(counts []int) []int {
	flipped := make([]int, 0, len(counts))
	for _, count := range slices.Backward(counts) {
		flipped = append(flipped, count)
	}

	return flipped
}

func runSuite(t *testing.T, runner tfexec.Runner, warm string, filters []string) {
	t.Helper()

	result, err := runner.Test(context.Background(), warm, tfexec.TestOptions{
		TestDirectory: "tests",
		Filters:       filters,
		Timeout:       0,
	})
	if err != nil {
		t.Fatalf("terraform test: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("terraform test exited %d for filters %v", result.ExitCode, filters)
	}
}

// fitCostModel solves for the fixed per-invocation cost and the marginal
// per-run-block cost by least squares over the unsplit sweep.
func fitCostModel(observations []timing) (fixed, marginal float64) {
	// Two points are the minimum a line can be fitted through.
	const minimumObservations = 2

	count := float64(len(observations))
	if len(observations) < minimumObservations {
		return 0, 0
	}

	sumX, sumY, sumXY, sumXX := 0.0, 0.0, 0.0, 0.0

	for _, observation := range observations {
		blocks := float64(observation.RunBlocks)
		seconds := observation.FastestSeconds

		sumX += blocks
		sumY += seconds
		sumXY += blocks * seconds
		sumXX += blocks * blocks
	}

	denominator := count*sumXX - sumX*sumX
	if denominator == 0 {
		return 0, 0
	}

	marginal = (count*sumXY - sumX*sumY) / denominator
	fixed = (sumY - marginal*sumX) / count

	return fixed, marginal
}

// measureVerboseCost is experiment two: the marginal cost of -verbose measured
// through this harness rather than inferred from the round-one review.
func measureVerboseCost(t *testing.T, runner tfexec.Runner, warm string, measured *costModel) {
	t.Helper()

	writeSuite(t, warm, costModelBlockCountFour, false)

	measured.Plain = []timing{timeArgs(t, runner, warm, costModelBlockCountFour,
		[]string{"test", "-json", "-no-color", "-test-directory=tests"}, &measured.PlainBytes)}
	measured.Verbose = []timing{timeArgs(t, runner, warm, costModelBlockCountFour,
		[]string{"test", "-json", "-no-color", "-verbose", "-test-directory=tests"}, &measured.VerboseBytes)}

	t.Logf("plain %.2fs / %d bytes, verbose %.2fs / %d bytes",
		measured.Plain[0].FastestSeconds, measured.PlainBytes,
		measured.Verbose[0].FastestSeconds, measured.VerboseBytes)
}

func timeArgs(t *testing.T, runner tfexec.Runner, warm string, blocks int, args []string, bytes *int) timing {
	t.Helper()

	fastest := time.Duration(0)
	slowest := time.Duration(0)

	if _, err := runner.Run(context.Background(), warm, args...); err != nil {
		t.Fatalf("warming terraform %v: %v", args, err)
	}

	for repetition := range costModelRepetitions {
		result, err := runner.Run(context.Background(), warm, args...)
		if err != nil {
			t.Fatalf("terraform %v: %v", args, err)
		}

		if result.ExitCode != 0 {
			t.Fatalf("terraform %v exited %d", args, result.ExitCode)
		}

		*bytes = len(result.Stdout)

		if repetition == 0 || result.Duration < fastest {
			fastest = result.Duration
		}

		if result.Duration > slowest {
			slowest = result.Duration
		}
	}

	return timing{
		RunBlocks:      blocks,
		Invocations:    1,
		FastestSeconds: fastest.Seconds(),
		SlowestSeconds: slowest.Seconds(),
	}
}

// measureSelectionYield is experiment three: how many run-block executions
// would instantiation-reachability selection actually remove?
//
// The bound computed here is the module-level one M1 already decides
// statically: a run block can only instantiate a mutated block if it targets
// the block's module or a module that calls it. Finer-grained reachability —
// count and for_each conditions — can only remove more, and is measured
// separately below where a fixture exercises it.
func measureSelectionYield(t *testing.T) []yield {
	t.Helper()

	fixtures := []string{awsMockedFixture, "nocoverage", "upward", "discriminate"}
	results := make([]yield, 0, len(fixtures))

	for _, fixture := range fixtures {
		path := filepath.Join(fixtureRoot, fixture)
		if fixture == "upward" {
			path = filepath.Join(path, "root")
		}

		configuration, err := discovery.Discover(path, "tests")
		if err != nil {
			t.Fatalf("discovering %s: %v", fixture, err)
		}

		results = append(results, yieldFor(fixture, configuration))
	}

	return results
}

func yieldFor(fixture string, configuration discovery.Configuration) yield {
	total := len(configuration.Tests.Runs)
	sites := 0
	selected := 0

	for _, module := range configuration.Modules {
		count := len(module.Outputs) + len(module.Locals) + len(module.Resources) + len(module.DataSources)
		sites += count
		selected += count * runsTargeting(configuration, module)
	}

	executions := sites * total

	removed := 0.0
	if executions > 0 {
		removed = float64(executions-selected) / float64(executions)
	}

	return yield{
		Fixture:           fixture,
		Mutants:           sites,
		TotalRunBlocks:    total,
		SelectedRunBlocks: selected,
		RemovedShare:      removed,
	}
}

// runsTargeting counts the run blocks that instantiate the module.
func runsTargeting(configuration discovery.Configuration, module discovery.Module) int {
	targeting := 0

	for _, run := range configuration.Tests.Runs {
		target, found := configuration.ModuleByRel(configuration.RootRelative())
		if !found {
			continue
		}

		if run.ModuleSource != "" {
			dir := filepath.Clean(filepath.Join(configuration.ModuleDir, run.ModuleSource))

			candidate, ok := configuration.ModuleByDir(dir)
			if !ok {
				continue
			}

			target = candidate
		}

		if reaches(configuration, target, module) {
			targeting++
		}
	}

	return targeting
}

func reaches(configuration discovery.Configuration, from, wanted discovery.Module) bool {
	if from.Dir == wanted.Dir {
		return true
	}

	for _, call := range from.Calls {
		if !call.Local {
			continue
		}

		child, found := configuration.ModuleByDir(call.Dir)
		if !found {
			continue
		}

		if reaches(configuration, child, wanted) {
			return true
		}
	}

	return false
}

// prepareCostModelWorkspace copies and initialises the fixture once, so that
// every timing below measures execution rather than installation.
func prepareCostModelWorkspace(t *testing.T, runner tfexec.Runner, module, workDir string) string {
	t.Helper()

	warm := filepath.Join(workDir, "cost-model")
	if err := os.CopyFS(warm, os.DirFS(module)); err != nil {
		t.Fatalf("copying fixture: %v", err)
	}

	if err := runner.Init(context.Background(), warm, false); err != nil {
		t.Fatalf("terraform init: %v", err)
	}

	return warm
}

// writeSuite replaces the fixture's test directory with count independent
// plan-mode run blocks, either in one file or one file each.
func writeSuite(t *testing.T, warm string, count int, split bool) {
	t.Helper()

	tests := filepath.Join(warm, "tests")
	if err := os.RemoveAll(tests); err != nil {
		t.Fatalf("clearing test directory: %v", err)
	}

	if err := os.MkdirAll(tests, 0o750); err != nil {
		t.Fatalf("creating test directory: %v", err)
	}

	blocks := make([]string, 0, count)
	for index := range count {
		blocks = append(blocks, runBlock(index))
	}

	if split {
		for index, block := range blocks {
			name := fmt.Sprintf("run%02d.tftest.hcl", index)
			writeFile(t, filepath.Join(tests, name), mockHeader+block)
		}

		return
	}

	writeFile(t, filepath.Join(tests, "unit.tftest.hcl"), mockHeader+strings.Join(blocks, ""))
}

const mockHeader = `mock_provider "aws" {}

`

func runBlock(index int) string {
	return fmt.Sprintf(`run "case_%02d" {
  command = plan

  assert {
    condition     = output.tier == "standard"
    error_message = "development workloads are standard tier"
  }
}

`, index)
}

func publishCostModel(t *testing.T, measured costModel) {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		return
	}

	directory := filepath.Join(root, ".artifacts", "performance")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("creating %s: %v", directory, err)
	}

	encoded, err := json.MarshalIndent(measured, "", "  ")
	if err != nil {
		t.Fatalf("encoding measurement: %v", err)
	}

	path := filepath.Join(directory, "m2-cost-model.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
