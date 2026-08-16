// Package engine is the single seam of the tool: configuration in, report out.
//
// Everything the command line does is a thin shell over Run, and every test of
// product behaviour drives this entry point against the real Terraform binary.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// Minimum Terraform versions. The test framework arrived in 1.6 and provider
// mocking — the mode this tool is built for — in 1.7.
const (
	minimumMajor = 1
	minimumMinor = 6
	mockingMinor = 7
)

// Defaults for the tuning knobs.
const (
	// DefaultTestDirectory matches Terraform's own default.
	DefaultTestDirectory = "tests"
	// DefaultTimeoutFactor multiplies the baseline run time.
	DefaultTimeoutFactor = 10.0
	// DefaultTimeoutFloor is the lower bound on a mutant's execution budget.
	DefaultTimeoutFloor = 30 * time.Second
	// DefaultTerraformBinary is the executable looked up on PATH.
	DefaultTerraformBinary = "terraform"
	// jobsNumerator and jobsDenominator set the default share of the machine.
	jobsNumerator   = 3
	jobsDenominator = 4
)

// defaultJobs saturates the machine without drowning it: three quarters of the
// cores, and never fewer than one. Parallelism is worth 3x on a provider-free
// module and almost nothing against a large provider schema (see
// docs/research/06-m1-exit-gate.md), so taking every core buys little and
// costs the rest of the desktop.
func defaultJobs() int {
	return max(1, runtime.NumCPU()*jobsNumerator/jobsDenominator)
}

// Config is the complete input to a mutation run.
type Config struct {
	// ModuleDir is the module to mutate.
	ModuleDir string
	// TestDirectory is the test directory relative to ModuleDir.
	TestDirectory string
	// Jobs bounds the number of mutants executing concurrently.
	Jobs int
	// TimeoutFactor multiplies the baseline duration to bound a mutant.
	TimeoutFactor float64
	// TimeoutFloor is the lower bound on that budget.
	TimeoutFloor time.Duration
	// MinScore is the mutation score percentage the run must reach.
	MinScore float64
	// HasMinScore reports whether MinScore was requested.
	HasMinScore bool
	// AllowIncompleteScore lets a timeout-affected score satisfy MinScore.
	AllowIncompleteScore bool
	// AllowRealInfrastructure permits execution against unmocked providers.
	AllowRealInfrastructure bool
	// AllowUnsandboxedEffects permits apply-mode execution of provisioners and
	// the data sources mocking does not sever.
	AllowUnsandboxedEffects bool
	// Preview generates the mutant population without executing anything.
	Preview bool
	// TerraformBinary is the Terraform executable to drive.
	TerraformBinary string
	// Env adds environment entries to every Terraform invocation.
	Env []string
	// WorkDir is the parent of the run's temporary directory.
	WorkDir string
	// TestSelection restricts mutant execution to the named test files. It is
	// the seam test selection will use; the milestone leaves it empty, which
	// runs the whole suite for every mutant.
	TestSelection []string
	// Tier is the operator breadth band.
	Tier mutation.Tier
	// IncludeOperators, when non-empty, restricts generation to these operators.
	IncludeOperators []string
	// ExcludeOperators removes operators from the population.
	ExcludeOperators []string
}

func (c Config) withDefaults() Config {
	if c.TestDirectory == "" {
		c.TestDirectory = DefaultTestDirectory
	}

	if c.Jobs <= 0 {
		c.Jobs = defaultJobs()
	}

	if c.TimeoutFactor <= 0 {
		c.TimeoutFactor = DefaultTimeoutFactor
	}

	if c.TimeoutFloor <= 0 {
		c.TimeoutFloor = DefaultTimeoutFloor
	}

	if c.TerraformBinary == "" {
		c.TerraformBinary = DefaultTerraformBinary
	}

	return c
}

// Operational failures. Every one of them aborts the run: none of them can be
// reported as a mutant verdict without misleading the reader.
var (
	// ErrTerraformVersion reports a Terraform release the tool cannot drive.
	ErrTerraformVersion = errors.New("unsupported Terraform version")
	// ErrRealInfrastructure reports an unmocked provider.
	ErrRealInfrastructure = errors.New("refusing to run against real infrastructure")
	// ErrUnsandboxedEffects reports apply-mode execution of local side effects.
	ErrUnsandboxedEffects = errors.New("refusing to execute unsandboxed effects")
	// ErrBaselineRed reports a test suite that does not pass unmutated.
	ErrBaselineRed = errors.New("baseline test suite is not green")
	// ErrBaselineNoRuns reports a baseline that executed no run blocks.
	ErrBaselineNoRuns = errors.New("baseline executed no run blocks")
)

// Run performs a complete mutation run and returns the report.
func Run(ctx context.Context, config Config) (report.Report, error) {
	config = config.withDefaults()

	runner := tfexec.Runner{Binary: config.TerraformBinary, Env: config.Env}

	moduleDir, err := filepath.Abs(config.ModuleDir)
	if err != nil {
		return report.Report{}, fmt.Errorf("resolving module directory: %w", err)
	}

	version, err := checkVersion(ctx, runner, moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	configuration, err := discovery.Discover(moduleDir, config.TestDirectory)
	if err != nil {
		return report.Report{}, err
	}

	// The gates guard execution, and a preview executes nothing. Refusing a
	// preview would only hide the population from the person deciding whether
	// to accept the risk.
	warnings := make([]string, 0, 1)

	if !config.Preview {
		warnings, err = checkSafety(configuration, config)
		if err != nil {
			return report.Report{}, err
		}
	}

	if len(configuration.Tests.Runs) == 0 {
		return report.Report{}, fmt.Errorf("%w: %s declares no run blocks",
			ErrBaselineNoRuns, configuration.Tests.Dir)
	}

	workRoot, err := os.MkdirTemp(config.WorkDir, "tf-mut-")
	if err != nil {
		return report.Report{}, fmt.Errorf("creating work directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, err := prepare(ctx, runner, configuration, config, workRoot)
	if err != nil {
		return report.Report{}, err
	}

	warnings = append(warnings, prepared.warnings...)

	generated, err := mutation.Generator{
		Configuration: configuration,
		Schemas:       prepared.schemas,
		Selection:     config.selection(),
	}.Generate()
	if err != nil {
		return report.Report{}, err
	}

	warnings = append(warnings, generated.Warnings...)

	result := shell(configuration, config, version.Terraform, moduleDir, prepared, warnings)
	mutants := describe(configuration, generated.Mutants)

	if config.Preview {
		result.Mutants = mutants
		result.Metrics = report.ComputeMetrics(nil)

		return result, nil
	}

	executed, failures := execute(ctx, executionPlan{
		runner:        runner,
		configuration: configuration,
		config:        config,
		prepared:      prepared,
		generated:     generated.Mutants,
		described:     mutants,
		workRoot:      workRoot,
		closure:       configuration.BuildClosure(),
	})

	result.Mutants = executed
	result.Errors = failures
	result.Metrics = report.ComputeMetrics(executed)
	result.OperatorErrors = report.ComputeOperatorErrors(executed)
	result.Findings = findings(configuration, executed)
	result.Warnings = append(result.Warnings, unanswerableResources(configuration, executed)...)

	return result, nil
}

// shell builds the report value's context: everything true of the run before
// any mutant has a verdict.
func shell(
	configuration discovery.Configuration,
	config Config,
	terraformVersion, moduleDir string,
	prepared warm,
	warnings []string,
) report.Report {
	return report.Report{
		SchemaVersion:    report.SchemaVersion,
		Command:          commandName(config),
		Module:           moduleDir,
		TerraformVersion: terraformVersion,
		TestDirectory:    configuration.TestDirRelative(),
		Baseline: report.Baseline{
			Runs:               prepared.baselineRuns,
			Assertions:         countAssertions(configuration),
			DurationMS:         prepared.baselineDuration.Milliseconds(),
			Fingerprint:        baselineFingerprint(prepared),
			VolatileComponents: prepared.mask.Paths(),
		},
		Mutants:        []report.Mutant{},
		Findings:       []report.Finding{},
		Metrics:        report.Metrics{}, //nolint:exhaustruct // replaced once mutants have verdicts.
		OperatorErrors: []report.OperatorErrors{},
		Suppressions:   []report.Suppression{},
		Warnings:       warnings,
		Errors:         []report.ExecutionError{},
	}
}

func commandName(config Config) report.Command {
	if config.Preview {
		return report.CommandPreview
	}

	return report.CommandRun
}

func checkVersion(ctx context.Context, runner tfexec.Runner, dir string) (tfexec.Version, error) {
	version, err := runner.Version(ctx, dir)
	if err != nil {
		return tfexec.Version{}, err
	}

	if !version.AtLeast(minimumMajor, minimumMinor) {
		return tfexec.Version{}, fmt.Errorf(
			"%w: found %s, but the terraform test framework requires %d.%d or later "+
				"(provider mocking requires %d.%d)",
			ErrTerraformVersion, version.Terraform, minimumMajor, minimumMinor, minimumMajor, mockingMinor,
		)
	}

	return version, nil
}

func countAssertions(configuration discovery.Configuration) int {
	total := 0
	for _, run := range configuration.Tests.Runs {
		total += run.Assertions
	}

	return total
}

// describe converts generated mutants into report values, assigning the
// statically decidable NoCoverage state before anything executes.
func describe(configuration discovery.Configuration, generated []mutation.Mutant) []report.Mutant {
	exercised := configuration.ExercisedModules()
	described := make([]report.Mutant, 0, len(generated))

	for _, mutant := range generated {
		state := report.Pending
		if !exercised[mutant.ModuleRel] {
			state = report.NoCoverage
		}

		described = append(described, report.Mutant{
			ID:       mutant.ID,
			Operator: string(mutant.Operator),
			Tier:     string(mutation.TierOf(mutant.Operator)),
			Module:   mutant.ModuleRel,
			Site:     mutant.Site,
			Resource: mutant.Resource,
			Range: report.Range{
				File:  mutant.File,
				Start: report.Position{Line: mutant.Range.Start.Line, Column: mutant.Range.Start.Column},
				End:   report.Position{Line: mutant.Range.End.Line, Column: mutant.Range.End.Column},
			},
			Diff:  mutant.Diff,
			State: state,
			Runs:  []report.RunOutcome{},
		})
	}

	return described
}

// selection is the operator population the configuration asks for.
func (c Config) selection() mutation.Selection {
	tier := c.Tier
	if !tier.Valid() {
		tier = mutation.TierStandard
	}

	return mutation.Selection{Tier: tier, Include: c.IncludeOperators, Exclude: c.ExcludeOperators}
}

// baselineFingerprint composes the unmutated suite's masked fingerprint, which
// is what every mutant is compared against.
func baselineFingerprint(prepared warm) string {
	masked := make([]fingerprint.Payload, 0, len(prepared.payloads))

	for _, payload := range prepared.payloads {
		projected, _ := prepared.mask.Apply(payload)
		masked = append(masked, projected)
	}

	return fingerprint.Fingerprint(masked)
}
