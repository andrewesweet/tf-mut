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
	"runtime/pprof"
	"slices"
	"strings"
	"time"

	"github.com/andrewesweet/tf-mut/internal/buildinfo"
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
	// ProgressIntervalForTesting overrides the cadence for engine-seam tests.
	// Zero selects the ten-second default; honoured only when Progress is set.
	ProgressIntervalForTesting time.Duration

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
	errCPUProfileInModule = errors.New("CPU profile destination is inside the module directory")

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

const cpuProfileFileMode = 0o600

// Run performs a complete mutation run and returns the report.
func Run(ctx context.Context, request Request) (result report.Report, err error) {
	started := time.Now()
	var collector *runCollector
	defer func() {
		if err != nil || collector == nil ||
			result.Command == report.CommandCharacterise || result.Command == report.CommandTodos {
			return
		}

		result.Telemetry = collector.finish()
		result.Telemetry.WallMS = time.Since(started).Milliseconds()
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
		listed, listErr := discovery.DiscoverWith(moduleDir, s.TestDirectory,
			discovery.Options{SkipJSON: disableJSONReading(s), CheckoutRoot: checkoutRoot(ctx, moduleDir)})
		if listErr != nil {
			return report.Report{}, listErr
		}

		return listTodos(listed, s, "")
	}

	runner := tfexec.Runner{Binary: s.TerraformBinary, Env: s.Env}

	versionPhase := collector.phase(phaseVersion)
	version, err := checkVersion(ctx, runner, moduleDir)
	versionPhase(collector.child("version", version.Usage, version.Started)...)
	if err != nil {
		return report.Report{}, err
	}

	discoveryPhase := collector.phase(phaseDiscovery)
	configuration, err := discovery.DiscoverWith(moduleDir, s.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(s), CheckoutRoot: checkoutRoot(ctx, moduleDir)})
	discoveryPhase()
	if err != nil {
		return report.Report{}, err
	}

	// Characterisation judges safety gates against its planned suite rather than the one on disk.
	// Request types choose dispatch; characterise and todos publish no telemetry.
	switch request.(type) {
	case CurateRequest, *CurateRequest:
		return curateSuite(ctx, runner, configuration, s, version, moduleDir, collector)
	case CharacteriseRequest, *CharacteriseRequest:
		return characteriseModule(ctx, runner, configuration, s, version, collector)
	default:
		// run, preview and suggest share the grading pipeline: the mode each
		// request carries decides preview's shortcuts and the suggest leg.
		// The population fact stays inside the application layer; the seam's
		// consumers receive its published report projection.
		result, _, err := mutate(ctx, runner, configuration, s, version, moduleDir, collector)

		return result, err
	}
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

func startCPUProfile(moduleDir, destination string, warnings io.Writer) (func(), error) {
	profilePath, profileErr := cpuProfilePath(moduleDir, destination)
	if profileErr != nil {
		return nil, profileErr
	}

	//nolint:gosec // caller-selected diagnostic file; O_EXCL refuses existing files and final symlinks.
	profileFile, profileErr := os.OpenFile(profilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, cpuProfileFileMode)
	if profileErr != nil {
		return nil, fmt.Errorf("opening the CPU profile file: %w", profileErr)
	}

	writer := &cpuProfileWriter{Writer: profileFile}
	if startErr := pprof.StartCPUProfile(writer); startErr != nil {
		_ = profileFile.Close()
		return nil, fmt.Errorf("starting the CPU profile: %w", startErr)
	}

	return func() {
		pprof.StopCPUProfile()
		if profileErr := errors.Join(writer.err, profileFile.Close()); profileErr != nil && warnings != nil {
			_, _ = fmt.Fprintf(warnings, "tf-mut: CPU profile may be incomplete: %s\n",
				strings.ReplaceAll(profileErr.Error(), "\n", "; "))
		}
	}, nil
}

func cpuProfilePath(moduleDir, destination string) (string, error) {
	module, err := filepath.Abs(moduleDir)
	if err != nil {
		return "", fmt.Errorf("resolving module directory for the CPU profile: %w", err)
	}

	module, err = filepath.EvalSymlinks(module)
	if err != nil {
		return "", fmt.Errorf("resolving module directory for the CPU profile: %w", err)
	}

	path, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolving the CPU profile destination: %w", err)
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolving the CPU profile parent directory: %w", err)
	}

	path = filepath.Join(parent, filepath.Base(path))

	if within(module, path) {
		return "", fmt.Errorf("%w: %q", errCPUProfileInModule, destination)
	}

	return path, nil
}

type cpuProfileWriter struct {
	io.Writer

	err error
}

// Write forwards the profile bytes to the wrapped writer and remembers the
// first error, so the caller can report it once after the run completes.
func (w *cpuProfileWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}

	if w.err == nil {
		w.err = err
	}

	return n, err
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
		result.Metrics = projectMetrics(
			oracle.ComputeMetrics(oracle.NewPopulation(nil, oracle.Provenance{})),
		)

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

	return result, population, nil
}

// gradingProjection is the report projection's subscriber state: what each
// published conclusion contributes to the report, held until the report is
// projected once, last.
type gradingProjection struct {
	plan        executionPlan
	moduleDir   string
	shell       report.Report
	evaluated   evaluatedPopulation
	completed   completedRun
	suggestions []report.Suggestion
	cost        string
	labels      map[int]string
}

// completeReport computes the aggregates over the classified population.
func (p *gradingProjection) completeReport(event PopulationClassified) error {
	finalisePhase := p.plan.collector.phase(phaseFinalise)
	p.completed = completeReport(p.plan.configuration, p.plan.settings, p.plan.populationDescribed, p.shell,
		p.evaluated.rows, event.population, p.evaluated.trip)
	finalisePhase()

	return nil
}

// accept applies the baseline gate to the classified population.
func (p *gradingProjection) accept(event PopulationClassified) error {
	labels, err := applyBaselineGate(p.plan.settings, p.moduleDir, p.shell, p.evaluated.rows,
		event.population, p.completed.gates)
	p.labels = labels

	return err
}

// subscribeSuggestions projects each suggestion outcome in publication order,
// which is survivor order, and the verification cost statement.
func (p *gradingProjection) subscribeSuggestions(events *dispatcher) {
	p.suggestions = []report.Suggestion{}

	events.suggestionProposed.subscribe(func(event SuggestionProposed) error {
		p.suggestions = append(p.suggestions, projectCandidate(event.candidate))

		return nil
	})
	events.suggestionVerified.subscribe(func(event SuggestionVerified) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.suggestionRefuted.subscribe(func(event SuggestionRefuted) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.suggestionSkipped.subscribe(func(event SuggestionSkipped) error {
		p.suggestions = append(p.suggestions, projectSuggestion(event.suggestion))

		return nil
	})
	events.verificationCosted.subscribe(func(event VerificationCosted) error {
		p.cost = event.costs.Statement()

		return nil
	})
}

// project attaches the aggregate results computed over the population fact.
// Per-mutant projections already accompany their classified rows; the shell's
// selection, sampling, population split and suppressions pass through without
// becoming inputs to the outcome arithmetic.
func (p *gradingProjection) project() report.Report {
	result := p.shell
	result.Mutants = p.evaluated.rows
	result.Errors = p.evaluated.failures
	result.Metrics = p.completed.metrics
	result.OperatorErrors = p.completed.operatorErrors
	result.Findings = p.completed.findings
	result.Warnings = append(result.Warnings, p.completed.warnings...)
	if p.cost != "" {
		result.Warnings = append(result.Warnings, p.cost)
	}

	result.Gates = p.completed.gates
	result.Suggestions = p.suggestions

	for index, label := range p.labels {
		if label != "" {
			labelFinding(&result.Mutants[index], label)
		}
	}

	return result
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
		cache.store(evaluated.rows)
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
	trip *tripOutcome,
) completedRun {
	metrics := projectMetrics(oracle.ComputeMetrics(population))

	return completedRun{
		metrics:        metrics,
		operatorErrors: projectOperatorErrors(oracle.ComputeOperatorErrors(population)),
		findings:       findingsOf(configuration, executed, population),
		warnings: append(append(unanswerableResources(configuration, populationDescribed),
			unscoredWarnings(population, metrics)...), tripWarnings(trip)...),
		gates: gateOutcomes(settings, shell, metrics),
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
// The counts are the run's own published provenance split, plus the two
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
) (tfexec.Version, error) {
	version, err := runner.Version(ctx, dir)
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

// conditionallyNoCoverageClaim is the finding a pre-classified mutant
// carries: the reason the oracle's NoCoverage constructor states for it. The
// fact itself — that the block is never instantiated — is published by
// discovery's ConditionalMultiplicity.
const conditionallyNoCoverageClaim = "the mutated block's multiplicity expression is statically zero under every " +
	"relevant run, so no run instantiates the block and nothing can execute the mutation"

// describeMutants converts generated mutants into report values, assigning the
// statically decidable states before anything executes: module-level
// NoCoverage first — the structural precedence and the M2 behaviour — then
// the guarded static Unobservable shortcut (M3a.2).
//
// Every described row leaves with the outcome classified for it, so the
// population fact the run reports is built at the classification sites
// themselves and never re-parsed from the published values.
func describeMutants(
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
func (s settings) Exclude() tfconfig.Exclude {
	return tfconfig.Exclude{Paths: s.ExcludePaths, Resources: s.ExcludeResources}
}

// toolVersion is what a generated file's header records: this binary's own
// version, never Terraform's. The two were confused once, which put a
// Terraform version in a line reading "Generated by tf-mut characterise".
func (s settings) toolVersion() string {
	return buildinfo.Resolve(s.ToolVersion)
}

func (s settings) withDefaults() settings {
	if s.TestDirectory == "" {
		s.TestDirectory = DefaultTestDirectory
	}

	if s.Jobs <= 0 {
		s.Jobs = defaultJobs()
	}

	if s.TimeoutFactor <= 0 {
		s.TimeoutFactor = DefaultTimeoutFactor
	}

	if s.TimeoutFloor <= 0 {
		s.TimeoutFloor = DefaultTimeoutFloor
	}

	if s.TerraformBinary == "" {
		s.TerraformBinary = DefaultTerraformBinary
	}

	if s.FailureTripThreshold <= 0 {
		s.FailureTripThreshold = DefaultFailureTripThreshold
	}

	return s
}

// selection is the operator population the configuration asks for.
func (s settings) selection() mutation.Selection {
	tier := s.Tier
	if !tier.Valid() {
		tier = mutation.TierStandard
	}

	return mutation.Selection{
		Tier: tier, Include: s.IncludeOperators, Exclude: s.ExcludeOperators,
		GeneratedFamilies: s.GeneratedFunctions,
	}
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
