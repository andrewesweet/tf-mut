// Package engine is the single seam of the tool: configuration in, report out.
//
// Everything the command line does is a thin shell over Run, and every test of
// product behaviour drives this entry point against the real Terraform binary.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	tfconfig "github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"

	"github.com/andrewesweet/tf-mut/internal/tracing"
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
	// DefaultInitTimeout gives provider and module downloads a longer cap
	// than the other preflight calls while still bounding a stalled download.
	DefaultInitTimeout = 10 * time.Minute
	// DefaultPreflightTimeout bounds preflight invocations other than init.
	DefaultPreflightTimeout = time.Minute
	// DefaultTerraformBinary is the executable looked up on PATH.
	DefaultTerraformBinary = "terraform"
	// DefaultFailureTripThreshold is the number of consecutive operational
	// failures after which a run stops dispatching.
	DefaultFailureTripThreshold = 5
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
	// PreflightTimeout uses zero to preserve the distinct init and non-init
	// defaults. Finalisation rejects negative values before any command runs.
	PreflightTimeout time.Duration
	// MinScore is the mutation score percentage the run must reach.
	MinScore float64
	// HasMinScore reports whether MinScore was requested.
	HasMinScore bool
	// AllowIncompleteScore lets an incomplete score satisfy MinScore.
	AllowIncompleteScore bool
	// AllowRealInfrastructure permits execution against unmocked providers.
	AllowRealInfrastructure bool
	// AllowUnsandboxedEffects permits apply-mode execution of provisioners and
	// the data sources mocking does not sever.
	AllowUnsandboxedEffects bool
	// FailureTripThreshold is the number of consecutive operational failures
	// after which the run stops dispatching: a dependency failing for every
	// mutant is reported after a few failures instead of after the whole
	// population. The trip fires at this threshold or the pending population,
	// whichever is smaller.
	FailureTripThreshold int
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

	// CPUProfile names the file the command's own CPU profile is written to.
	// Empty — the default — profiles nothing.
	CPUProfile string
	// OTLPEndpoint is the OTLP/HTTP endpoint the run's spans are exported to.
	// Empty means unconfigured; the OTel environment variable is honoured
	// then, resolved by the tracing package.
	OTLPEndpoint string
	// Progress carries Common.Progress; see that field's output contract.
	Progress io.Writer

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

// traceFlushTimeout caps the deferred OTLP flush, so a run whose collector
// died never hangs on the way out.
const traceFlushTimeout = 5 * time.Second

// Run performs a complete mutation run and returns the report.
func Run(ctx context.Context, request Request) (result report.Report, err error) {
	started := time.Now()
	var collector *runCollector
	defer func() {
		result, err = finishRunReport(collector, started, result, err)
		if err == nil {
			err = ctx.Err()
		}
	}()

	s, err := settingsFor(request)
	if err != nil {
		return report.Report{}, err
	}

	stopProfile, profileErr := prepareRunOutput(&s)
	if profileErr != nil {
		return report.Report{}, profileErr
	}

	defer stopProfile()

	// The flush outlives request cancellation so recorded spans can still reach the collector.
	// Tracing and CPU-profile warnings share the request's progress writer —
	// never a report sink — and a nil writer drops them.
	tracer := tracing.New(ctx, s.OTLPEndpoint, s.Progress)
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), traceFlushTimeout)
		defer cancel()
		_ = tracer.Shutdown(flushCtx)
	}()

	// Telemetry records its own overhead without affecting verdicts.
	collector = newRunCollector(ctx, tracer)
	//nolint:contextcheck // beginRun returns the run-span context on purpose: the
	// pipeline below runs under it, so every span it starts parents correctly.
	ctx = collector.beginRun(string(commandName(s.mode)))
	defer collector.endRun()

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
		listed, listErr := discoverConfiguration(ctx, moduleDir, s)
		if listErr != nil {
			return report.Report{}, listErr
		}

		if cancelErr := ctx.Err(); cancelErr != nil {
			return report.Report{}, cancelErr
		}

		return listTodos(listed, s, "")
	}

	runner := tfexec.Runner{Binary: s.TerraformBinary, Env: s.Env}

	versionPhase := collector.phase(phaseVersion)
	version, err := checkVersion(ctx, runner, moduleDir, s.preflightCap())
	versionPhase(collector.child("version", version.Usage, version.Started)...)
	if err != nil {
		return report.Report{}, err
	}

	discoveryPhase := collector.phase(phaseDiscovery)
	configuration, err := discoverConfiguration(ctx, moduleDir, s)
	discoveryPhase()
	if err != nil {
		return report.Report{}, err
	}

	// Characterisation judges safety gates against its planned suite rather than the one on disk.
	// Request types choose dispatch; characterise and todos publish no telemetry.
	switch typed := request.(type) {
	case CurateRequest, *CurateRequest:
		return curateSuite(ctx, runner, configuration, s, version, moduleDir, collector)
	case CharacteriseRequest:
		return characteriseModule(ctx, runner, configuration, s, version, collector, typed.pinDefectHooks)
	case *CharacteriseRequest:
		return characteriseModule(ctx, runner, configuration, s, version, collector, typed.pinDefectHooks)
	default:
		// run, preview and suggest share the grading pipeline: the mode each
		// request carries decides preview's shortcuts and the suggest leg.
		// The population fact stays inside the application layer; the seam's
		// consumers receive its published report projection.
		result, _, err := mutate(ctx, runner, configuration, s, version, moduleDir, collector)

		return result, err
	}
}

func discoverConfiguration(ctx context.Context, moduleDir string, s settings) (discovery.Configuration, error) {
	root, err := checkoutRoot(ctx, moduleDir)
	if err != nil {
		return discovery.Configuration{}, err
	}

	return discovery.DiscoverWith(moduleDir, s.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(s), CheckoutRoot: root})
}

func prepareRunOutput(s *settings) (func(), error) {
	if s.Progress != nil {
		s.Progress = &synchronizedWriter{writer: s.Progress}
	}

	// The CPU profile writes only to the file the user explicitly requested.
	if s.CPUProfile == "" {
		return func() {}, nil
	}

	return startCPUProfile(s.ModuleDir, s.CPUProfile, s.Progress)
}

func finishRunReport(
	collector *runCollector, started time.Time, result report.Report, err error,
) (report.Report, error) {
	if collector != nil && collector.trip != nil {
		if err != nil {
			err = fmt.Errorf("%w\nwarning: %s", err, strings.Join(tripWarnings(collector.trip), "\n"))
		} else {
			result.Warnings = append(result.Warnings, tripWarnings(collector.trip)...)
		}
	}

	if err != nil || collector == nil ||
		result.Command == report.CommandCharacterise || result.Command == report.CommandTodos {
		return result, err
	}

	result.Telemetry = collector.finish()
	result.Telemetry.WallMS = time.Since(started).Milliseconds()

	return result, err
}

// applyGates runs the static safety gates and the JSON floor's static half.
// A run is refused at a gate; a preview executes nothing and cannot be
// refused, so it carries the gates' verdict in the warnings instead: a pass
// contributes the same advisories a run would, a refusal contributes a
// warning quoting that refusal in full.
func applyGates(
	configuration discovery.Configuration,
	settings settings,
	collector *runCollector,
) (settings, []string, error) {
	if settings.mode != previewMode {
		gatePhase := collector.phase(phaseGates)
		checked, err := checkSafety(configuration, settings)
		if err != nil {
			return settings, nil, err
		}

		gated, warnings := applyFloor(configuration, settings, checked)
		gatePhase()

		return gated, warnings, nil
	}

	checked, gateErr := checkSafety(configuration, settings)
	if gateErr != nil {
		checked = append(checked, "the safety gates would refuse a mutation run: "+gateErr.Error())
	}

	gated, warnings := applyFloor(configuration, settings, checked)

	return gated, warnings, nil
}

// mutate is the grading pipeline: gates, generation, selection, execution and
// the completed report.
func mutate(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings settings, version tfexec.Version,
	moduleDir string,
	collector *runCollector,
) (report.Report, oracle.Population, error) {
	// The gates guard execution, and a preview executes nothing. Refusing a
	// preview would only hide the population from the person deciding whether
	// to accept the risk.
	settings, warnings, err := applyGates(configuration, settings, collector)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	workRoot, err := prepareWorkRoot(configuration, settings)
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, generated, err := build(ctx, runner, configuration, settings, workRoot, collector)
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
	result := reportShell(configuration, settings, version.Terraform, moduleDir, prepared, warnings)

	mutants, classified := describeMutants(configuration, graph, settings, generated.Mutants)

	// Suppression runs after the safety gates by construction: the gates are
	// decided from the parsed configuration before generation, so no exclusion
	// can reach them. The suppression is classified onto the fact in place:
	// the rows travel with the described mutants they align with.
	mutants, suppressions, rejected := suppress(configuration,
		settings.Exclude(), mutants, classified)
	result.Suppressions = suppressions
	result.Warnings = append(result.Warnings, rejected...)

	// The count levers: --since and --sample scope the population, and the
	// report says distinctly what was selected and what was left out. The
	// described population before selection is what coverage claims are judged
	// over, so the plan carries it beside the selected rows.
	selectionPhase := collector.phase(phaseSelection)
	selected, err := applyCountLevers(ctx, configuration, settings, &result, mutants,
		generated.Mutants, classified)
	selectionPhase()
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	if settings.mode == previewMode {
		result.Mutants = selected.mutants
		result.Metrics = emptyMetrics()

		return result, oracle.Population{}, nil
	}

	plan := executionPlan{
		runner:              runner,
		configuration:       configuration,
		settings:            settings,
		prepared:            prepared,
		generated:           selected.generated,
		described:           selected.mutants,
		populationDescribed: mutants,
		classified:          selected.classified,
		workRoot:            workRoot,
		closure:             configuration.BuildClosure(),
		graph:               graph,
		collector:           collector,
		events:              &dispatcher{},
	}

	executionPhase := collector.phase(phaseExecution)
	evaluated, err := executeWithCache(ctx, &result, version, plan)
	executionPhase()
	if err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	return finishReport(ctx, plan, moduleDir, result, evaluated)
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

// finishReport publishes the classified population and projects the report from
// what its subscribers concluded.
//
// The subscription order is the contract (ADR-0005): the report projection
// completes the aggregates first, the suggestion leg reacts next in suggest
// mode, and the acceptance gate last, so a suggestion leg that fails stops
// delivery before the baseline writer runs.
func finishReport(
	ctx context.Context,
	plan executionPlan,
	moduleDir string,
	shell report.Report,
	evaluated evaluatedPopulation,
) (report.Report, oracle.Population, error) {
	if err := ctx.Err(); err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	population := classifyRun(shell, evaluated)
	projection := &gradingProjection{plan: plan, moduleDir: moduleDir, shell: shell, evaluated: evaluated}

	plan.events.populationClassified.subscribe(projection.completeReport)

	if plan.settings.mode == suggestMode {
		projection.subscribeSuggestions(plan.events)
		plan.events.populationClassified.subscribe(func(PopulationClassified) error {
			return suggestAssertions(ctx, plan, evaluated.rows)
		})
	}

	plan.events.populationClassified.subscribe(func(event PopulationClassified) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		return projection.accept(event)
	})

	if err := plan.events.populationClassified.publish(PopulationClassified{population: population}); err != nil {
		return report.Report{}, oracle.Population{}, err
	}

	result := projection.project()
	if plan.settings.mode == suggestMode {
		applySuggestions(plan.settings, &result, applyContext{
			moduleDir:   plan.configuration.ModuleDir,
			closureRoot: plan.configuration.ClosureRoot,
			testDirs:    []string{plan.configuration.Tests.Dir, plan.configuration.ModuleDir},
		})
	}

	return result, population, ctx.Err()
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
) (evaluatedPopulation, error) {
	loadPhase := plan.collector.phase(phaseCacheLoad)
	cache := openCache(plan.configuration, plan.settings, plan.prepared, terraform)
	if cache != nil {
		result.Population.Cached = cache.load(plan.described, plan.classified)
		result.Population.Fresh = result.Population.Selected - result.Population.Cached
	}

	loadPhase()

	evaluated, err := execute(ctx, plan)
	if err != nil {
		return evaluatedPopulation{}, err
	}

	if evaluated.trip != nil {
		result.Population.Fresh -= evaluated.trip.unexecuted
	}

	if err := ctx.Err(); err != nil {
		return evaluatedPopulation{}, err
	}

	storePhase := plan.collector.phase(phaseCacheStore)
	if cache != nil {
		cache.store(evaluated.rows, evaluated.classified)
	}

	storePhase()

	return evaluated, nil
}

// finalise applies the configured exclusions and defaults, and refuses the
// gate combinations the truth table forbids before any work is done.
func finalise(s settings, configured tfconfig.File) (settings, error) {
	s = s.withDefaults()
	s.ExcludePaths = append(slices.Clone(s.ExcludePaths), configured.Exclude.Paths...)
	s.ExcludeResources = append(slices.Clone(s.ExcludeResources), configured.Exclude.Resources...)

	if s.PreflightTimeout < 0 {
		return settings{}, fmt.Errorf("%w: the preflight timeout %s is negative", tfconfig.ErrConfig, s.PreflightTimeout)
	}

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
	collector *runCollector,
) (warmWorkspace, mutation.Result, error) {
	prepared, err := prepare(ctx, runner, configuration, settings, workRoot, collector)
	if err != nil {
		return warmWorkspace{}, mutation.Result{}, err // nothing was built.
	}

	generationPhase := collector.phase(phaseGeneration)
	generated, err := mutation.Generator{
		Configuration: configuration,
		Schemas:       prepared.schemas,
		Selection:     settings.selection(),
		Packs:         settings.loadedPacks,
		TestDirectory: configuration.Tests.Dir,
		Defect:        seedGenerationDefect(settings),
	}.Generate()
	generationPhase()

	return prepared, generated, err
}

// completeReport computes everything the report can only say once every
// mutant has a verdict, over the population fact the classification sites
// assembled. It returns values; finishReport projects them onto the published
// report once, last.
func completeReport(
	configuration discovery.Configuration,
	settings settings,
	populationDescribed []report.Mutant,
	shell report.Report,
	executed []report.Mutant,
	population oracle.Population,
) completedRun {
	metrics := projectMetrics(oracle.ComputeMetrics(population))

	return completedRun{
		metrics:        metrics,
		operatorErrors: projectOperatorErrors(oracle.ComputeOperatorErrors(population)),
		findings:       findingsOf(configuration, executed, population),
		warnings: append(unanswerableResources(configuration, populationDescribed),
			unscoredWarnings(population, metrics)...),
		gates: gateOutcomes(settings, shell, metrics, population),
	}
}

// completedRun is what a finished execution concluded, computed as values over
// the population fact and projected onto the published report once, in
// finishReport.
type completedRun struct {
	metrics        report.Metrics
	operatorErrors []report.OperatorErrors
	findings       []report.Finding
	warnings       []string
	gates          *report.Gates
}

// classifyRun mints the run's population fact: the classified rows the
// classification sites produced, with the provenance the gates judge them by.
//
// The counts are the run's own published provenance split, plus the
// observation counts the observation proof refuses on. Timed-out rows are
// counted from the fact's own members, so the proof can never disagree with
// the population it judges. Execution failures are counted separately:
// affected rows retain their last outcome without proving full observation.
func classifyRun(
	shell report.Report,
	evaluated evaluatedPopulation,
) oracle.Population {
	timedOut := 0

	for _, row := range evaluated.classified {
		if row.Outcome.State() == oracle.StateTimeout {
			timedOut++
		}
	}

	unexecuted := 0
	if evaluated.trip != nil {
		unexecuted = evaluated.trip.unexecuted
	}

	return oracle.NewPopulation(evaluated.classified, oracle.Provenance{
		Cached:     shell.Population.Cached,
		TimedOut:   timedOut,
		Errored:    len(evaluated.failures),
		Unexecuted: unexecuted,
		Tripped:    evaluated.trip != nil,
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
// min-score row is recorded here, under the population authority and count-lever
// rules documented in docs/design/product-design.md, section 10.
func gateOutcomes(
	settings settings,
	shell report.Report,
	metrics report.Metrics,
	population oracle.Population,
) *report.Gates {
	// Full selection alone does not prove full observation; the oracle must
	// establish authority before the gate can claim the Full row.
	_, authErr := population.Authoritative(ErrBaselineUnobserved, "")
	partial := authErr != nil || shell.Sampling != nil ||
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

// reportShell builds the report value's context: everything true of the run
// before any mutant has a verdict.
func reportShell(
	configuration discovery.Configuration,
	settings settings, terraformVersion, moduleDir string,
	prepared warmWorkspace,
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
		Metrics:        report.Metrics{}, // replaced once mutants have verdicts.
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

func checkVersion(
	ctx context.Context,
	runner tfexec.Runner,
	dir string,
	limit time.Duration,
) (tfexec.Version, error) {
	version, err := runner.Version(ctx, dir, limit)
	if err != nil {
		return version, err
	}

	if !version.AtLeast(minimumMajor, minimumMinor) {
		return version, fmt.Errorf(
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

// baselineFingerprint composes the unmutated suite's masked fingerprint, which
// is what every mutant is compared against.
func baselineFingerprint(prepared warmWorkspace) string {
	masked := make([]fingerprint.Payload, 0, len(prepared.payloads))

	for _, payload := range prepared.payloads {
		projected, _ := prepared.mask.Apply(payload)
		masked = append(masked, projected)
	}

	return fingerprint.Fingerprint(masked)
}
