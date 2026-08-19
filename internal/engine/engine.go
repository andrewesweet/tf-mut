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
	"slices"
	"time"

	"github.com/andrewesweet/tf-mut/internal/buildinfo"
	"github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
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
	// ExcludePaths and ExcludeResources remove sites from the population.
	ExcludePaths     []string
	ExcludeResources []string
	// SetFlags names the flags the caller set explicitly, so that a configured
	// scalar can be overridden per scalar rather than per file.
	SetFlags []string
	// Since scopes the run to configuration changed since the git ref: the
	// union of the committed range, the working tree and untracked files.
	Since string
	// SamplePercent selects a deterministic sample of the population.
	SamplePercent float64
	// HasSample reports whether sampling was requested.
	HasSample bool
	// SampleSeed seeds the deterministic sample.
	SampleSeed int64
	// AllowSampledGate is the separately named unsafe opt-in without which a
	// sampled run can satisfy no gate.
	AllowSampledGate bool
	// NoCache disables cache reads and writes: the documented mitigation for
	// the plan-values-on-disk risk, and the escape hatch for a shared cache.
	NoCache bool
	// FailOnNew fails the run on any finding the baseline does not accept.
	FailOnNew bool
	// WriteBaseline accepts the current findings as the baseline; permitted
	// only on a full unsampled population.
	WriteBaseline bool
	// BaselinePath overrides the baseline file location, relative to the
	// module directory unless absolute.
	BaselinePath string
	// GeneratedFunctions opts in to the generated function catalogue (M3e):
	// FN-FAMILY-SWAP joins the population. Never part of standard until the
	// published admission measurement, in a separate change.
	GeneratedFunctions bool
	// DisableStaticShortcuts turns the static pre-classifications off — the
	// static Unobservable shortcut and the conditional-instantiation
	// NoCoverage evaluator — so a control run can prove each shortcut equal
	// to the executed verdict. It is a seam control, not a command-line flag.
	DisableStaticShortcuts bool
	// DisableJSONReading leaves every JSON-syntax file in the closure unread,
	// so a control run can prove the safety floor holds for content the tool
	// has not read. It is a seam control, not a command-line flag.
	DisableJSONReading bool
	// Suggest generates, and unless SuggestDryRun is set verifies, the
	// assertion that would have killed each provable survivor.
	Suggest bool
	// SuggestDryRun prints the candidate patches and verifies nothing.
	SuggestDryRun bool
	// SurvivorIDs restricts suggestion to the named survivors. A missing or
	// stale identifier is an operational failure that names it.
	SurvivorIDs []string
	// Apply writes the named verified suggestions into the module's test
	// files, under the snapshot-bound protocol. Empty unless ApplyAll is set.
	Apply []string
	// ApplyAll writes every verified suggestion.
	ApplyAll bool
	// SeedSuggestionDefect makes the generator emit one deliberately wrong
	// assertion, so the suggestion-soundness gate can prove that verification
	// rejects it. It is a seam control, not a command-line flag.
	SeedSuggestionDefect suggest.Defect
	// ToolVersion is this binary's own version, recorded in the header of every
	// generated file. Empty in a seam test, where the development marker
	// stands in for it.
	ToolVersion string
	// Characterise scaffolds, harvests and pins a suite for a module that has
	// none, instead of grading the suite it has.
	Characterise bool
	// PinRung is the granularity ladder level: outputs, counts or configured.
	PinRung string
	// CharacteriseWrite places the verified suite in the module's test
	// directory. Without it the generated content is printed and nothing on
	// disk changes.
	CharacteriseWrite bool
	// CharacteriseForce replaces target files, and only those the provenance
	// registry marks generated-and-unmodified.
	CharacteriseForce bool
	// SeedMissingMock removes one planned provider-configuration mock from the
	// staged suite, so the staged provider gate can be proven to refuse before
	// execution. It is a seam control, not a command-line flag.
	SeedMissingMock string
	// Todos lists the open judgement points and runs no Terraform.
	Todos bool
	// Curate reports redundancy over an authoritative population.
	Curate bool
	// UntilDry iterates scaffold, mutate and pin until the survivors stop
	// yielding new assertions at the chosen granularity.
	UntilDry bool
	// Answers supplies TODO answers as todo-<id>=<value>.
	Answers []string
	// Resume reads answered TODOs from the edited non-executable artefact as
	// well as from Answers, re-synthesises, verifies and promotes.
	Resume bool
	// SeedSharedFileOrder stages every generated scenario into one file, in
	// the named order (forward or reverse), so the scaffold-soundness gate can
	// prove the pins are identical whatever the file order. It is a seam
	// control, not a command-line flag.
	SeedSharedFileOrder string
	// SeedClosureChange appends to a module-relative file immediately before the
	// first atomic rename of a characterisation write, so the input-closure
	// race the commit step exists to close can be staged at the probe. It is a
	// seam control, not a command-line flag.
	SeedClosureChange string
	// SeedClosureFile adds a module-relative Terraform file immediately before
	// the first atomic rename, so the half of the input-closure race that
	// *grows* the closure can be staged at the probe. It is a seam control,
	// not a command-line flag.
	SeedClosureFile string
	// SeedClosureAfter delays the seeded closure change until this many files
	// have already been renamed, so a *partial* commit can be staged rather
	// than a refused one. It is a seam control, not a command-line flag.
	SeedClosureAfter int
	// SeedUntilDryRounds bounds the until-dry loop, so the `bounded` exit —
	// the loop stopping because it ran out of rounds rather than because it
	// went dry — can be staged. It is a seam control, not a command-line flag.
	SeedUntilDryRounds int
	// SeedFinalPinDefect adds a knowingly false pin to the set the loop ends
	// with, so the verification that stands between the loop and the write can
	// be proven load-bearing rather than assumed. It is a seam control, not a
	// command-line flag.
	SeedFinalPinDefect bool
	// SeedInitialPinDefect does the same for the *first* verification, the one
	// between the harvest and everything downstream of it. The two verifiers
	// are separate code paths and only the later one had a seam, so deleting
	// the earlier one left the suite green. It is a seam control, not a
	// command-line flag.
	SeedInitialPinDefect bool
	// SeedRenameWindowChange fires the seeded closure change inside the rename
	// window rather than before it — after the temporary file has been
	// created, written, closed and chmodded — so a probe that ran only before
	// that window can be shown to miss it. It is a seam control, not a
	// command-line flag.
	SeedRenameWindowChange bool
	// SeedRegistryFailure makes the provenance registry fail to store, so the
	// one window in which every generated file has already been renamed can be
	// staged. It is a seam control, not a command-line flag.
	SeedRegistryFailure bool
	// SeedNoEscalation suppresses the zero-output auto-escalation, so the other
	// half of the contract — a rung that pinned nothing may never report
	// complete — can be proven on its own. It is a seam control, not a
	// command-line flag.
	SeedNoEscalation bool
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
func Run(ctx context.Context, settings Config) (report.Report, error) {
	moduleDir, err := filepath.Abs(settings.ModuleDir)
	if err != nil {
		return report.Report{}, fmt.Errorf("resolving module directory: %w", err)
	}

	settings, configured, err := settings.withConfiguration(moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	settings, err = finalise(settings, configured)
	if err != nil {
		return report.Report{}, err
	}

	// `todos` is dispatched before the version gate, because it runs no
	// Terraform at all: the shipped skill promises a cheap local inspection an
	// agent calls every iteration, and making it unavailable when Terraform is
	// absent or broken would break the loop over a check it never needed.
	if settings.Todos {
		listed, listErr := discovery.DiscoverWith(moduleDir, settings.TestDirectory,
			discovery.Options{SkipJSON: settings.DisableJSONReading})
		if listErr != nil {
			return report.Report{}, listErr
		}

		return listTodos(listed, settings, "")
	}

	runner := tfexec.Runner{Binary: settings.TerraformBinary, Env: settings.Env}

	version, err := checkVersion(ctx, runner, moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	configuration, err := discovery.DiscoverWith(moduleDir, settings.TestDirectory,
		discovery.Options{SkipJSON: settings.DisableJSONReading})
	if err != nil {
		return report.Report{}, err
	}

	// Characterisation is the same machinery pointed the other way: it has no
	// suite to baseline, no population to grade, and its safety gates are
	// judged against the suite it plans rather than the one on disk.

	if settings.Curate {
		return curateSuite(ctx, runner, configuration, settings, version, moduleDir)
	}

	if settings.Characterise {
		return characteriseModule(ctx, runner, configuration, settings, version)
	}

	return mutate(ctx, runner, configuration, settings, version, moduleDir)
}

// mutate is the grading pipeline: gates, generation, selection, execution and
// the completed report.
func mutate(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	version tfexec.Version,
	moduleDir string,
) (report.Report, error) {
	// The gates guard execution, and a preview executes nothing. Refusing a
	// preview would only hide the population from the person deciding whether
	// to accept the risk.
	warnings := make([]string, 0, 1)

	if !settings.Preview {
		checked, err := checkSafety(configuration, settings)
		if err != nil {
			return report.Report{}, err
		}

		warnings = checked
	}

	settings, warnings = applyFloor(configuration, settings, warnings)

	workRoot, err := prepareWorkRoot(configuration, settings)
	if err != nil {
		return report.Report{}, err
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, generated, err := build(ctx, runner, configuration, settings, workRoot)
	if err != nil {
		return report.Report{}, err
	}

	warnings = append(append(warnings, prepared.warnings...), generated.Warnings...)

	graph := floorGraph(configuration)
	result := shell(configuration, settings, version.Terraform, moduleDir, prepared, warnings)
	mutants := describe(configuration, graph, settings, generated.Mutants)

	// Suppression runs after the safety gates by construction: the gates are
	// decided from the parsed configuration before generation, so no exclusion
	// can reach them.
	mutants, suppressions, rejected := suppress(configuration,
		settings.Exclude(), mutants)
	result.Suppressions = suppressions
	result.Warnings = append(result.Warnings, rejected...)

	// The count levers: --since and --sample scope the population, and the
	// report says distinctly what was selected and what was left out.
	selected, selectedGenerated, err := applyCountLevers(ctx, configuration, settings, &result,
		mutants, generated.Mutants)
	if err != nil {
		return report.Report{}, err
	}

	if settings.Preview {
		result.Mutants = selected
		result.Metrics = report.ComputeMetrics(nil)

		return result, nil
	}

	plan := executionPlan{
		runner:        runner,
		configuration: configuration,
		config:        settings,
		prepared:      prepared,
		generated:     selectedGenerated,
		described:     selected,
		workRoot:      workRoot,
		closure:       configuration.BuildClosure(),
		graph:         graph,
	}

	executed, failures := executeWithCache(ctx, &result, version, plan)

	return finish(ctx, plan, moduleDir, result, executed, failures)
}

// prepareWorkRoot refuses a suite with no run blocks and creates the run's
// temporary directory.
func prepareWorkRoot(configuration discovery.Configuration, settings Config) (string, error) {
	if len(configuration.Tests.Runs) == 0 {
		return "", fmt.Errorf("%w: %s declares no run blocks",
			ErrBaselineNoRuns, configuration.Tests.Dir)
	}

	workRoot, err := os.MkdirTemp(settings.WorkDir, "tf-mut-")
	if err != nil {
		return "", fmt.Errorf("creating work directory: %w", err)
	}

	return workRoot, nil
}

// applyFloor is the JSON safety floor's static half. It holds whether or not
// the gates were authorised, and in a preview too: a shortcut is a claim about
// the whole configuration, and part of the configuration was not read.
func applyFloor(
	configuration discovery.Configuration,
	settings Config,
	warnings []string,
) (Config, []string) {
	floor := floorOf(configuration)
	if floor.active() {
		settings.DisableStaticShortcuts = true
		warnings = append(warnings, floor.degradation())
	}

	return settings, warnings
}

// floorGraph is the whole-payload floor expressed as the run's graph: while
// unread JSON is present the graph is built from `.tf` syntax alone, so it
// would be missing edges without saying so, and every mapping must fail.
func floorGraph(configuration discovery.Configuration) *discovery.Graph {
	if floorOf(configuration).active() {
		return discovery.UnmappedGraph()
	}

	return configuration.BuildGraph()
}

// finish completes the report, generates and verifies any suggestions, and
// applies the baseline gate.
func finish(
	ctx context.Context,
	plan executionPlan,
	moduleDir string,
	result report.Report,
	executed []report.Mutant,
	failures []report.ExecutionError,
) (report.Report, error) {
	result = complete(plan.configuration, plan.config, result, executed, failures)

	if plan.config.Suggest {
		suggestions, cost, err := suggestAssertions(ctx, plan, result)
		if err != nil {
			return report.Report{}, err
		}

		result.Suggestions = suggestions
		if cost != "" {
			result.Warnings = append(result.Warnings, cost)
		}

		applySuggestions(plan.config, &result, applyContext{
			moduleDir:   plan.configuration.ModuleDir,
			closureRoot: plan.configuration.ClosureRoot,
			testDirs:    []string{plan.configuration.Tests.Dir, plan.configuration.ModuleDir},
		})
	}

	if err := applyBaselineGate(plan.config, moduleDir, &result); err != nil {
		return report.Report{}, err
	}

	return result, nil
}

// executeWithCache replays cached verdicts under the coarse correct key
// (M3b.2), executes what remains fresh, and stores the new verdicts. The
// cache is nil — no reads, no writes — under either unsafe opt-in and under
// --no-cache.
func executeWithCache(
	ctx context.Context,
	result *report.Report,
	terraform tfexec.Version,
	plan executionPlan,
) ([]report.Mutant, []report.ExecutionError) {
	cache := openCache(plan.configuration, plan.config, plan.prepared, terraform)
	if cache != nil {
		result.Population.Cached = cache.load(plan.described)
		result.Population.Fresh = result.Population.Selected - result.Population.Cached
	}

	executed, failures := execute(ctx, plan)

	if cache != nil {
		cache.store(executed)
	}

	return executed, failures
}

// finalise applies the configured exclusions and defaults, and refuses the
// gate combinations the truth table forbids before any work is done.
func finalise(settings Config, configured config.File) (Config, error) {
	settings = settings.withDefaults()
	settings.ExcludePaths = append(slices.Clone(settings.ExcludePaths), configured.Exclude.Paths...)
	settings.ExcludeResources = append(slices.Clone(settings.ExcludeResources), configured.Exclude.Resources...)

	// The gate truth table's sampled and write rows: refused before any work
	// is done.
	if err := checkSampledGate(settings); err != nil {
		return Config{}, err
	}

	if err := checkSuggestCombinations(settings); err != nil {
		return Config{}, err
	}

	if err := checkCuratePopulation(settings); err != nil {
		return Config{}, err
	}

	if err := checkUntilDryPopulation(settings); err != nil {
		return Config{}, err
	}

	if err := checkBaselineWrite(settings); err != nil {
		return Config{}, err
	}

	return settings, nil
}

// applyCountLevers scopes the described population and records the selection,
// sampling and population facts on the report.
func applyCountLevers(
	ctx context.Context,
	configuration discovery.Configuration,
	settings Config,
	result *report.Report,
	mutants []report.Mutant,
	generated []mutation.Mutant,
) ([]report.Mutant, []mutation.Mutant, error) {
	chosen, err := selectPopulation(ctx, configuration, settings, mutants)
	if err != nil {
		return nil, nil, err
	}

	selected, selectedGenerated, population := chosen.apply(mutants, generated)
	result.Selection = chosen.metadata
	result.Sampling = chosen.sampling
	result.Population = population

	return selected, selectedGenerated, nil
}

// build warms one workspace and generates the population from it.
func build(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	workRoot string,
) (warm, mutation.Result, error) {
	prepared, err := prepare(ctx, runner, configuration, settings, workRoot)
	if err != nil {
		return warm{}, mutation.Result{}, err //nolint:exhaustruct // nothing was built.
	}

	generated, err := mutation.Generator{
		Configuration: configuration,
		Schemas:       prepared.schemas,
		Selection:     settings.selection(),
	}.Generate()

	return prepared, generated, err
}

// complete fills in everything the report can only say once every mutant has a
// verdict.
func complete(
	configuration discovery.Configuration,
	settings Config,
	result report.Report,
	executed []report.Mutant,
	failures []report.ExecutionError,
) report.Report {
	result.Mutants = executed
	result.Errors = failures
	result.Metrics = report.ComputeMetrics(executed)
	result.OperatorErrors = report.ComputeOperatorErrors(executed)
	result.Findings = findings(configuration, executed)
	result.Warnings = append(result.Warnings, unanswerableResources(configuration, executed)...)
	result.Gates = gateOutcomes(settings, result)

	return result
}

// scopeLabel names the population a gate was evaluated over.
func scopeLabel(full bool) string {
	if full {
		return "full"
	}

	return "selected"
}

// gateOutcomes records the gate table's outcomes for this run (2.1.0). The
// fail-on-new and baseline rows arrive with the baseline file (M3b.3); the
// min-score row is recorded here, labelled partial over any scoped or sampled
// population.
func gateOutcomes(settings Config, result report.Report) *report.Gates {
	// The gate table's non-Full rows: scoped, sampled, or served even partly
	// from the cache — each evaluated over what actually ran, labelled
	// partial.
	partial := result.Sampling != nil ||
		result.Population.Cached > 0 ||
		(result.Selection.Mode == report.SelectionSince && result.Selection.ForcedFull == "")
	scope := scopeLabel(!partial)

	minScore := report.GateOutcome{
		Evaluated: settings.HasMinScore,
		Scope:     "", Partial: false, Passed: false, Refused: "",
	}

	if settings.HasMinScore {
		minScore.Scope = scope
		minScore.Partial = partial
		minScore.Passed = (!result.Metrics.Incomplete || settings.AllowIncompleteScore) &&
			result.Metrics.MutationScore*100 >= settings.MinScore
	}

	return &report.Gates{
		MinScore:  minScore,
		FailOnNew: report.GateOutcome{Evaluated: false, Scope: "", Partial: false, Passed: false, Refused: ""},
		Baseline:  nil,
	}
}

// shell builds the report value's context: everything true of the run before
// any mutant has a verdict.
func shell(
	configuration discovery.Configuration,
	settings Config,
	terraformVersion, moduleDir string,
	prepared warm,
	warnings []string,
) report.Report {
	return report.Report{
		SchemaVersion:    report.SchemaVersion,
		Command:          commandName(settings),
		Module:           moduleDir,
		ClosureRoot:      configuration.ClosureRoot,
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

func commandName(settings Config) report.Command {
	switch {
	case settings.Todos:
		return report.CommandTodos
	case settings.Curate:
		return report.CommandCurate
	case settings.Characterise:
		return report.CommandCharacterise
	case settings.Preview:
		return report.CommandPreview
	case settings.Suggest:
		return report.CommandSuggest
	default:
		return report.CommandRun
	}
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
// statically decidable states before anything executes: module-level
// NoCoverage first — the structural precedence and the M2 behaviour — then
// the guarded static Unobservable shortcut (M3a.2).
func describe(
	configuration discovery.Configuration,
	graph *discovery.Graph,
	settings Config,
	generated []mutation.Mutant,
) []report.Mutant {
	exercised := configuration.ExercisedModules()
	described := make([]report.Mutant, 0, len(generated))

	for _, mutant := range generated {
		state := report.Pending

		var verdict *report.Verdict

		switch {
		case !exercised[mutant.ModuleRel]:
			state = report.NoCoverage
		case !settings.Preview && !settings.DisableStaticShortcuts &&
			conditionallyUncovered(configuration, graph, settings, mutant):
			// The finer conditional-instantiation claim (M3a.3): the mutated
			// multiplicity expression is statically zero under every relevant
			// run. Module-level NoCoverage above remains the strict subset.
			state = report.NoCoverage
			verdict = conditionalNoCoverageVerdict()
		case !settings.Preview && !settings.DisableStaticShortcuts &&
			staticallyUnobservable(graph, mutant):
			// A preview keeps Pending — the documented preview contract — so
			// the shortcut fires only where execution would otherwise run.
			state = report.Unobservable
			verdict = staticUnobservableVerdict()
		default:
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
			Diff:    mutant.Diff,
			State:   state,
			Verdict: verdict,
			Runs:    []report.RunOutcome{},
		})
	}

	return described
}

// staticallyUnobservable applies the guarded static shortcut (M3a.2, review
// C2). The structural precedence comes first: a non-projecting operator —
// every contract and ordering operator — is never statically classified, and
// execution decides its state. The shortcut then fires only where the site
// maps into the graph (unmapped fails closed to execution) and its forward
// cone reaches nothing observable: no resource, data source, output, check or
// contract construct.
func staticallyUnobservable(graph *discovery.Graph, mutant mutation.Mutant) bool {
	if !mutation.Projects(mutant.Operator) {
		return false
	}

	cone, ok := graph.SiteCone(mutant.ModuleRel, mutant.Site)
	if !ok {
		return false
	}

	return !cone.ContainsObservable()
}

// staticUnobservableVerdict is the finding a statically classified mutant
// carries: the same claim the executed verdict would make, reached without
// the execution.
func staticUnobservableVerdict() *report.Verdict {
	return &report.Verdict{
		Diagnosis: "",
		Message: "the mutated node's forward cone reaches no resource, data source, output, " +
			"check or contract construct, so no plan or state could reflect the change and " +
			"no assertion could ever read it",
		Fix: "either the construct is genuinely dead — delete it — or nothing consumes it " +
			"yet: wire it into a resource or an output and re-run",
		//nolint:exhaustruct // no delta exists: nothing executed.
		Evidence: report.Evidence{ClosureVerdict: "statically unobservable: empty observable cone"},
	}
}

// Exclude is the site exclusion policy the run was given.
func (c Config) Exclude() config.Exclude {
	return config.Exclude{Paths: c.ExcludePaths, Resources: c.ExcludeResources}
}

// toolVersion is what a generated file's header records: this binary's own
// version, never Terraform's. The two were confused once, which put a
// Terraform version in a line reading "Generated by tf-mut characterise".
func (c Config) toolVersion() string {
	return buildinfo.Resolve(c.ToolVersion)
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

// selection is the operator population the configuration asks for.
func (c Config) selection() mutation.Selection {
	tier := c.Tier
	if !tier.Valid() {
		tier = mutation.TierStandard
	}

	return mutation.Selection{
		Tier: tier, Include: c.IncludeOperators, Exclude: c.ExcludeOperators,
		GeneratedFamilies: c.GeneratedFunctions,
	}
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

// RuleDescriptions renders the operator catalogue for the reporters, so that a
// SARIF rule carries the catalogue's own words rather than a paraphrase.
func RuleDescriptions() map[string]report.RuleDescription {
	descriptions := make(map[string]report.RuleDescription, len(mutation.Catalogue()))

	for id, entry := range mutation.Descriptions() {
		descriptions[id] = report.RuleDescription{
			Tier:        string(entry.Tier),
			Description: entry.Description,
			Killer:      entry.Killer,
		}
	}

	return descriptions
}
