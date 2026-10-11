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
	tfconfig "github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
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

// settings is the complete input to one engine command: the internal settings
// value each request type produces. The request's type is the command, and
// the mode field below is its projection here — both are unexported, so an
// inapplicable command and option combination is not representable through
// the seam at all.
type settings struct {
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
	// Packs names the selected domain packs: the union of the flag and the
	// configured list, deduplicated by name. Selection is by name only;
	// packs are resolved and loaded by the configuration step, whose product
	// is loadedPacks below.
	Packs []string
	// loadedPacks are the selected packs, loaded and checked against the
	// contract.
	loadedPacks []mutation.Pack
	// staticShortcutsDisabled records the invocation-local JSON safety floor.
	staticShortcutsDisabled bool
	// SuggestDryRun prints the candidate patches and verifies nothing. It is
	// the suggest command's --dry-run; verification is the default.
	SuggestDryRun bool
	// SurvivorIDs restricts suggestion to the named survivors. A missing or
	// stale identifier is an operational failure that names it.
	SurvivorIDs []string
	// Apply writes the named verified suggestions into the module's test
	// files, under the snapshot-bound protocol. Empty unless ApplyAll is set.
	Apply []string
	// ApplyAll writes every verified suggestion.
	ApplyAll bool
	// ToolVersion is this binary's own version, recorded in the header of every
	// generated file. Empty in a seam test, where the development marker
	// stands in for it.
	ToolVersion string
	// PinRung is the granularity ladder level: outputs, counts or configured.
	PinRung string
	// CharacteriseWrite places the verified suite in the module's test
	// directory. Without it the generated content is printed and nothing on
	// disk changes.
	CharacteriseWrite bool
	// CharacteriseForce replaces target files, and only those the provenance
	// registry marks generated-and-unmodified.
	CharacteriseForce bool
	// UntilDry iterates scaffold, mutate and pin until the survivors stop
	// yielding new assertions at the chosen granularity.
	UntilDry bool
	// Answers supplies TODO answers as todo-<id>=<value>.
	Answers []string
	// Resume reads answered TODOs from the edited non-executable artefact as
	// well as from Answers, re-synthesises, verifies and promotes.
	Resume bool

	// mode is the command these settings serve: the projection of the request
	// type that produced them, written by that type's settings method and by
	// nothing else.
	mode mode
}

//nolint:gochecknoglobals // test seam, inert outside the suite.
var disableStaticShortcuts = func(settings) bool { return false }

//nolint:gochecknoglobals // test seam, inert outside the suite.
var disableJSONReading = func(settings) bool { return false }

// seedGenerationDefect is the inert test hook beside the generator it drives:
// the red proofs for the origins gate cases seed a deliberately wrong
// generation through it and nowhere else.
//
//nolint:gochecknoglobals // test seam, inert outside the suite.
var seedGenerationDefect = func(settings) mutation.Defect { return mutation.DefectNone }

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
func Run(ctx context.Context, request Request) (report.Report, error) {
	s, err := settingsFor(request)
	if err != nil {
		return report.Report{}, err
	}

	moduleDir, err := filepath.Abs(s.ModuleDir)
	if err != nil {
		return report.Report{}, fmt.Errorf("resolving module directory: %w", err)
	}

	s, configured, err := s.withConfiguration(moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	s, err = finalise(s, configured)
	if err != nil {
		return report.Report{}, err
	}

	// `todos` is dispatched before the version gate, because it runs no
	// Terraform at all: the shipped skill promises a cheap local inspection an
	// agent calls every iteration, and making it unavailable when Terraform is
	// absent or broken would break the loop over a check it never needed.
	switch request.(type) {
	case TodosRequest, *TodosRequest:
		listed, listErr := discovery.DiscoverWith(moduleDir, s.TestDirectory,
			discovery.Options{SkipJSON: disableJSONReading(s)})
		if listErr != nil {
			return report.Report{}, listErr
		}

		return listTodos(listed, s, "")
	}

	runner := tfexec.Runner{Binary: s.TerraformBinary, Env: s.Env}

	version, err := checkVersion(ctx, runner, moduleDir)
	if err != nil {
		return report.Report{}, err
	}

	configuration, err := discovery.DiscoverWith(moduleDir, s.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(s)})
	if err != nil {
		return report.Report{}, err
	}

	// Characterisation is the same machinery pointed the other way: it has no
	// suite to baseline, no population to grade, and its safety gates are
	// judged against the suite it plans rather than the one on disk. The
	// dispatch is on the request's type: the type is the mode.
	switch request.(type) {
	case CurateRequest, *CurateRequest:
		return curateSuite(ctx, runner, configuration, s, version, moduleDir)
	case CharacteriseRequest, *CharacteriseRequest:
		return characteriseModule(ctx, runner, configuration, s, version)
	default:
		// run, preview and suggest share the grading pipeline: the mode each
		// request carries decides preview's shortcuts and the suggest leg.
		// The population fact the pipeline returns is published report DTO
		// material; the seam's consumers read the report.
		result, _, err := mutate(ctx, runner, configuration, s, version, moduleDir)

		return result, err
	}
}

// mutate is the grading pipeline: gates, generation, selection, execution and
// the completed report.
func mutate(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings settings, version tfexec.Version,
	moduleDir string,
) (report.Report, oracle.Population, error) {
	// The gates guard execution, and a preview executes nothing. Refusing a
	// preview would only hide the population from the person deciding whether
	// to accept the risk.
	warnings := make([]string, 0, 1)

	if settings.mode != previewMode {
		checked, err := checkSafety(configuration, settings)
		if err != nil {
			return report.Report{}, oracle.Population{}, err
		}

		warnings = checked
	}

	settings, warnings = applyFloor(configuration, settings, warnings)

	workRoot, err := prepareWorkRoot(configuration, settings)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, generated, err := build(ctx, runner, configuration, settings, workRoot)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	warnings = append(append(warnings, prepared.warnings...), generated.Warnings...)

	// Preview's pack summary: an entry that found no site — no literal of its
	// form, no schema evidence, or a shape outside M5's scope — is a no-op,
	// and the person deciding what a pack covers is told so.
	if settings.mode == previewMode {
		warnings = append(warnings, describeUnmatched(generated.UnmatchedEntries)...)
	}

	graph := floorGraph(configuration)
	result := shell(configuration, settings, version.Terraform, moduleDir, prepared, warnings)

	mutants, classified := describe(configuration, graph, settings, generated.Mutants)

	// Suppression runs after the safety gates by construction: the gates are
	// decided from the parsed configuration before generation, so no exclusion
	// can reach them. The suppression is classified onto the fact in place:
	// the rows travel with the described mutants they align with.
	mutants, suppressions, rejected := suppress(configuration,
		settings.Exclude(), mutants, classified)
	result.Suppressions = suppressions
	result.Warnings = append(result.Warnings, rejected...)

	// The count levers: --since and --sample scope the population, and the
	// report says distinctly what was selected and what was left out.
	selected, err := applyCountLevers(ctx, configuration, settings, &result, mutants,
		generated.Mutants, classified)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	if settings.mode == previewMode {
		result.Mutants = selected.mutants
		result.Metrics = projectMetrics(
			oracle.ComputeMetrics(oracle.NewPopulation(nil, oracle.Provenance{})),
		)

		return result, oracle.Population{}, nil
	}

	plan := executionPlan{
		runner:        runner,
		configuration: configuration,
		settings:      settings,
		prepared:      prepared,
		generated:     selected.generated,
		described:     selected.mutants,
		classified:    selected.classified,
		workRoot:      workRoot,
		closure:       configuration.BuildClosure(),
		graph:         graph,
	}

	executed, classified, failures := executeWithCache(ctx, &result, version, plan)

	return finish(ctx, plan, moduleDir, result, executed, classified, failures)
}

// prepareWorkRoot refuses a suite with no run blocks and creates the run's
// temporary directory.
func prepareWorkRoot(configuration discovery.Configuration, settings settings) (string, error) {
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
	settings settings, warnings []string,
) (settings, []string) {
	floor := floorOf(configuration)
	if floor.active() {
		settings.staticShortcutsDisabled = true
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
	shell report.Report,
	executed []report.Mutant,
	classified []oracle.Classified,
	failures []report.ExecutionError,
) (report.Report, oracle.Population, error) {
	completed := complete(plan.configuration, plan.settings, shell, executed, classified, failures)

	suggestions := []report.Suggestion(nil)
	cost := ""

	if plan.settings.mode == suggestMode {
		var suggestErr error
		suggestions, cost, suggestErr = suggestAssertions(ctx, plan, executed)
		if suggestErr != nil {
			return report.Report{}, oracle.Population{}, suggestErr
		}
	}

	labels, err := applyBaselineGate(plan.settings, moduleDir, shell, executed,
		completed.population, completed.gates)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	// The one projection, last: everything the run concluded is spelled onto
	// the published report here and nowhere else. The shell's bookkeeping —
	// selection, sampling, the population split, suppressions — passes
	// through; the values computed over the population fact are attached.
	result := shell
	result.Mutants = executed
	result.Errors = failures
	result.Metrics = completed.metrics
	result.OperatorErrors = completed.operatorErrors
	result.Findings = completed.findings
	result.Warnings = append(result.Warnings, completed.warnings...)
	if cost != "" {
		result.Warnings = append(result.Warnings, cost)
	}

	result.Gates = completed.gates
	result.Suggestions = suggestions

	if plan.settings.mode == suggestMode {
		applySuggestions(plan.settings, &result, applyContext{
			moduleDir:   plan.configuration.ModuleDir,
			closureRoot: plan.configuration.ClosureRoot,
			testDirs:    []string{plan.configuration.Tests.Dir, plan.configuration.ModuleDir},
		})
	}

	for index, label := range labels {
		if label != "" {
			labelFinding(&result.Mutants[index], label)
		}
	}

	return result, completed.population, nil
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
) ([]report.Mutant, []oracle.Classified, []report.ExecutionError) {
	cache := openCache(plan.configuration, plan.settings, plan.prepared, terraform)
	if cache != nil {
		result.Population.Cached = cache.load(plan.described, plan.classified)
		result.Population.Fresh = result.Population.Selected - result.Population.Cached
	}

	executed, classified, failures := execute(ctx, plan)

	if cache != nil {
		cache.store(executed)
	}

	return executed, classified, failures
}

// finalise applies the configured exclusions and defaults, and refuses the
// gate combinations the truth table forbids before any work is done.
func finalise(s settings, configured tfconfig.File) (settings, error) {
	s = s.withDefaults()
	s.ExcludePaths = append(slices.Clone(s.ExcludePaths), configured.Exclude.Paths...)
	s.ExcludeResources = append(slices.Clone(s.ExcludeResources), configured.Exclude.Resources...)

	// The gate truth table's sampled and write rows: refused before any work
	// is done.
	if err := checkSampledGate(s); err != nil {
		return settings{}, err
	}

	if err := checkSuggestCombinations(s); err != nil {
		return settings{}, err
	}

	if err := checkCuratePopulation(s); err != nil {
		return settings{}, err
	}

	if err := checkUntilDryPopulation(s); err != nil {
		return settings{}, err
	}

	if err := checkBaselineWrite(s); err != nil {
		return settings{}, err
	}

	return s, nil
}

// selectedPopulation is what the count levers kept: the described rows, the
// generated mutants behind them, and the classified rows aligned with both,
// all in row order.
type selectedPopulation struct {
	mutants    []report.Mutant
	generated  []mutation.Mutant
	classified []oracle.Classified
}

// applyCountLevers scopes the described population and records the selection,
// sampling and population facts on the report.
func applyCountLevers(
	ctx context.Context,
	configuration discovery.Configuration,
	settings settings,
	result *report.Report,
	described []report.Mutant,
	generated []mutation.Mutant,
	classified []oracle.Classified,
) (selectedPopulation, error) {
	chosen, err := selectPopulation(ctx, configuration, settings, described)
	if err != nil {
		return selectedPopulation{}, err
	}

	selected, population := chosen.apply(described, generated, classified)
	result.Selection = chosen.metadata
	result.Sampling = chosen.sampling
	result.Population = population

	return selected, nil
}

// build warms one workspace and generates the population from it.
func build(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings settings, workRoot string,
) (warm, mutation.Result, error) {
	prepared, err := prepare(ctx, runner, configuration, settings, workRoot)
	if err != nil {
		return warm{}, mutation.Result{}, err //nolint:exhaustruct // nothing was built.
	}

	generated, err := mutation.Generator{
		Configuration: configuration,
		Schemas:       prepared.schemas,
		Selection:     settings.selection(),
		Packs:         settings.loadedPacks,
		TestDirectory: configuration.Tests.Dir,
		Defect:        seedGenerationDefect(settings),
	}.Generate()

	return prepared, generated, err
}

// complete computes everything the report can only say once every mutant has
// a verdict, over the population fact the classification sites assembled. It
// returns values; finish projects them onto the published report once, last.
func complete(
	configuration discovery.Configuration,
	settings settings,
	shell report.Report,
	executed []report.Mutant,
	classified []oracle.Classified,
	failures []report.ExecutionError,
) completedRun {
	population := classifyRun(shell, classified, failures)
	metrics := projectMetrics(oracle.ComputeMetrics(population))

	return completedRun{
		population:     population,
		metrics:        metrics,
		operatorErrors: projectOperatorErrors(oracle.ComputeOperatorErrors(population)),
		findings:       findingsOf(configuration, executed, population),
		warnings:       unanswerableResources(configuration, executed),
		gates:          gateOutcomes(settings, shell, metrics),
	}
}

// completedRun is what a finished execution concluded, computed as values over
// the population fact and projected onto the published report once, in
// finish.
type completedRun struct {
	population     oracle.Population
	metrics        report.Metrics
	operatorErrors []report.OperatorErrors
	findings       []report.Finding
	warnings       []string
	gates          *report.Gates
}

// classifyRun mints the run's population fact: the classified rows the
// classification sites produced, with the provenance the gates judge them by.
//
// The counts are the run's own published provenance split, plus the two
// observation counts the observation proof refuses on. Timed-out rows are
// counted from the fact's own members, so the proof can never disagree with
// the population it judges; the errored are the mutants that never reached a
// verdict and therefore carry no classified outcome at all.
func classifyRun(
	shell report.Report,
	classified []oracle.Classified,
	failures []report.ExecutionError,
) oracle.Population {
	timedOut := 0

	for _, row := range classified {
		if row.Outcome.State() == oracle.StateTimeout {
			timedOut++
		}
	}

	return oracle.NewPopulation(classified, oracle.Provenance{
		Cached:   shell.Population.Cached,
		TimedOut: timedOut,
		Errored:  len(failures),
	})
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
func gateOutcomes(settings settings, shell report.Report, metrics report.Metrics) *report.Gates {
	// The gate table's non-Full rows: scoped, sampled, or served even partly
	// from the cache — each evaluated over what actually ran, labelled
	// partial.
	partial := shell.Sampling != nil ||
		shell.Population.Cached > 0 ||
		(shell.Selection.Mode == report.SelectionSince && shell.Selection.ForcedFull == "")
	scope := scopeLabel(!partial)

	minScore := report.GateOutcome{
		Evaluated: settings.HasMinScore,
		Scope:     "", Partial: false, Passed: false, Refused: "",
	}

	if settings.HasMinScore {
		minScore.Scope = scope
		minScore.Partial = partial
		minScore.Passed = (!metrics.Incomplete || settings.AllowIncompleteScore) &&
			metrics.MutationScore*100 >= settings.MinScore
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
	settings settings, terraformVersion, moduleDir string,
	prepared warm,
	warnings []string,
) report.Report {
	return report.Report{
		SchemaVersion:    report.SchemaVersion,
		Command:          commandName(settings.mode),
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

func commandName(m mode) report.Command {
	//nolint:exhaustive // gradeMode is the default: the zero mode is an ordinary run.
	switch m {
	case todosMode:
		return report.CommandTodos
	case curateMode:
		return report.CommandCurate
	case characteriseMode:
		return report.CommandCharacterise
	case previewMode:
		return report.CommandPreview
	case suggestMode:
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

// conditionallyNoCoverageClaim is the finding a pre-classified mutant
// carries: the reason the oracle's NoCoverage constructor states for it. The
// fact itself — that the block is never instantiated — is published by
// discovery's ConditionalMultiplicity.
const conditionallyNoCoverageClaim = "the mutated block's multiplicity expression is statically zero under every " +
	"relevant run, so no run instantiates the block and nothing can execute the mutation"

// describe converts generated mutants into report values, assigning the
// statically decidable states before anything executes: module-level
// NoCoverage first — the structural precedence and the M2 behaviour — then
// the guarded static Unobservable shortcut (M3a.2).
//
// Every described row leaves with the outcome classified for it, so the
// population fact the run reports is built at the classification sites
// themselves and never re-parsed from the published values.
func describe(
	configuration discovery.Configuration,
	graph *discovery.Graph,
	settings settings,
	generated []mutation.Mutant,
) ([]report.Mutant, []oracle.Classified) {
	exercised := configuration.ExercisedModules()
	described := make([]report.Mutant, 0, len(generated))
	classified := make([]oracle.Classified, 0, len(generated))
	shortcutsDisabled := settings.staticShortcutsDisabled || disableStaticShortcuts(settings)

	for _, mutant := range generated {
		entry := report.Mutant{
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
			Runs:    []report.RunOutcome{},
			Origins: projectOrigins(mutant.Origins),
		}

		var outcome oracle.Outcome

		switch {
		case !exercised[mutant.ModuleRel]:
			// Module-level NoCoverage: the report states the absence as a
			// count and no finding, so the outcome carries the state and
			// nothing else.
			outcome = oracle.NoCoverage("")
		case settings.mode != previewMode && !shortcutsDisabled &&
			configuration.ConditionalMultiplicity(graph, settings.Env, discovery.Site{
				ModuleRel: mutant.ModuleRel,
				File:      mutant.File,
				Address:   mutant.Site,
				Resource:  mutant.Resource,
				Mutated:   mutant.Mutated,
			}):
			// The finer conditional-instantiation claim (M3a.3): the mutated
			// multiplicity expression is statically zero under every relevant
			// run. Module-level NoCoverage above remains the strict subset.
			outcome = oracle.NoCoverage(conditionallyNoCoverageClaim)
		case settings.mode != previewMode && !shortcutsDisabled &&
			staticallyUnobservable(graph, mutant):
			// A preview keeps Pending — the documented preview contract — so
			// the shortcut fires only where execution would otherwise run.
			outcome = oracle.StaticallyUnobservable()
		default:
			// Pending stands: the state every executable mutant carries until
			// something decides otherwise, and the only state a preview
			// reports.
			outcome = oracle.Pending()
		}

		described = append(described, project(entry, outcome))
		classified = append(classified, oracle.Classified{Operator: mutant.Operator, Outcome: outcome})
	}

	return described, classified
}

// projectOrigins projects the generator's origins onto the published DTO:
// present exactly when at least one pack entry contributed, never empty.
func projectOrigins(origins []mutation.Origin) []report.Origin {
	if len(origins) == 0 {
		return nil
	}

	projected := make([]report.Origin, 0, len(origins))
	for _, origin := range origins {
		projected = append(projected, report.Origin{
			Operator: string(origin.Operator), Pack: origin.Pack, Entry: origin.Entry,
		})
	}

	return projected
}

// describeUnmatched renders preview's pack summary, one line per entry that
// produced nothing.
func describeUnmatched(unmatched []mutation.Origin) []string {
	described := make([]string, 0, len(unmatched))
	for _, origin := range unmatched {
		described = append(described, fmt.Sprintf("pack %q: entry %q (%s) matched no site: no top-level "+
			"resource argument of its type and attribute holds its literal, or the schema does not "+
			"describe the attribute at the entry's type", origin.Pack, origin.Entry, origin.Operator))
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

// Exclude is the site exclusion policy the run was given.
func (c settings) Exclude() tfconfig.Exclude {
	return tfconfig.Exclude{Paths: c.ExcludePaths, Resources: c.ExcludeResources}
}

// toolVersion is what a generated file's header records: this binary's own
// version, never Terraform's. The two were confused once, which put a
// Terraform version in a line reading "Generated by tf-mut characterise".
func (c settings) toolVersion() string {
	return buildinfo.Resolve(c.ToolVersion)
}

func (c settings) withDefaults() settings {
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
func (c settings) selection() mutation.Selection {
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
