package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// ErrScaffoldRed reports a generated suite that did not pass in the sandbox.
//
// It is an operational failure and never a result: the whole point of
// verifying before writing is that a suite this tool puts on disk is green by
// construction, so a red scaffold is a defect in the generator rather than a
// finding about the module.
var ErrScaffoldRed = errors.New("the generated suite is not green")

// ErrWriteRefused reports a write the protocol would not perform.
var ErrWriteRefused = errors.New("refusing to write the generated suite")

// characterise scaffolds, harvests, pins and verifies a suite for a module.
//
// The order is the contract. The safety gates are evaluated against the
// effective staged suite — the module sources plus the planned scaffold —
// statically, before Terraform is asked to do anything; the scaffold is
// harvested twice so that a value the mock invented cannot be pinned as one
// the module computes; and the pinned suite is proven green in a sandbox
// before a single byte reaches the source tree.
func characteriseModule(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	version tfexec.Version,
) (report.Report, error) {
	rung, err := characterise.ParseRung(settings.PinRung)
	if err != nil {
		return report.Report{}, err
	}

	answers, err := collectAnswers(configuration, settings)
	if err != nil {
		return report.Report{}, err
	}

	// The gates first, and nothing before them but the version check. The
	// program they judge is the module sources plus the *planned* scaffold,
	// and which provider configurations that scaffold plans a mock for is
	// decided by discovery alone — so a refusal costs no `init`, no provider
	// download and no schema read, exactly as it does for a mutation run.
	planned := seedMissingMock(characterise.Configurations(configuration), settings)

	warnings, err := checkStagedSafety(configuration, planned, settings)
	if err != nil {
		return report.Report{}, err
	}

	workRoot, err := os.MkdirTemp(settings.WorkDir, "tf-mut-")
	if err != nil {
		return report.Report{}, fmt.Errorf("creating work directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, err := warmUp(ctx, runner, configuration, workRoot)
	if err != nil {
		return report.Report{}, err
	}

	scaffold := seedNoEscalation(characterise.Plan(configuration, prepared.schemas,
		characterise.Options{
			Rung:       rung,
			TestDirRel: configuration.TestDirRelative(),
			Version:    settings.toolVersion(),
			Sources:    prepared.sources,
			Answers:    answers,
		}, planned), settings)

	warnings = append(warnings, prepared.warnings...)

	result := shell(configuration, settings, version.Terraform,
		configuration.ModuleDir, prepared, warnings)
	result.Command = report.CommandCharacterise
	// A characterisation reads the whole module: there is no count lever it
	// could have been scoped by, and saying so keeps the population contract
	// the same shape for every command.
	result.Selection = report.Selection{Mode: scopeLabel(true), Ref: "", ForcedFull: ""}

	block, files, err := scaffoldSuite(ctx, runner, configuration, settings,
		workRoot, prepared, scaffold)
	if err != nil {
		return report.Report{}, err
	}

	// The until-dry loop grades what the scaffold pinned and pins whatever its
	// survivors still yield, over the staged suite: nothing on disk changes
	// until the caller asks for a write.
	if settings.UntilDry && block.Complete {
		if err := untilDry(ctx, runner, configuration, settings, version,
			&block, scaffold, workRoot); err != nil {
			return report.Report{}, err
		}

		promoted, refusals := promoteScaffolds(ctx, runner, configuration,
			prepared, &block, scaffold, answers, workRoot)
		result.Warnings = append(result.Warnings, refusals...)

		files = append(append(pinnedFiles(scaffold, block.Pins), promoted...),
			scaffoldArtefact(scaffold, &block)...)
		block.Files = entriesOf(files)
	}

	result.Characterisation = &block
	result.Metrics = report.ComputeMetrics(nil)

	if settings.CharacteriseWrite {
		if err := commitScaffold(configuration, settings, prepared, &block, files); err != nil {
			// The report travels with the failure. A commit that renamed one
			// file and then aborted has left the caller a partial state, and
			// an error alone would not say which files moved.
			if block.Write == nil || len(block.Write.Partial) == 0 {
				return report.Report{}, err
			}

			result.Warnings = append(result.Warnings, err.Error())

			return result, nil
		}
	}

	return result, nil
}

// scaffoldSuite harvests, pins and verifies the planned scaffold.
func scaffoldSuite(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	workRoot string,
	prepared warm,
	scaffold characterise.Scaffold,
) (report.Characterisation, []generated, error) {
	block := report.Characterisation{ //nolint:exhaustruct // filled in below, stage by stage.
		Rung: string(scaffold.Rung), Complete: false,
		Scenarios: scaffold.Scenarios, Pins: []report.Pin{}, Todos: scaffold.Todos,
		Files: []report.GeneratedFile{}, Staged: !settings.CharacteriseWrite,
	}

	if scaffold.Requested != scaffold.Rung {
		block.RungRequested = string(scaffold.Requested)
		block.Escalated = scaffold.Escalated
		block.EscalationReason = scaffold.EscalationReason
	}

	// An open judgement point means nothing executable can be produced: the
	// artefact is the editable surface, and promotion after verification is the
	// only route from it into test content.
	if openTodos(scaffold.Todos) > 0 {
		files := artefactFiles(scaffold, scaffold.Todos)
		block.Files = entriesOf(files)

		return block, files, nil
	}

	harvest, err := harvestScaffold(ctx, runner, configuration, workRoot, prepared, scaffold, settings)
	if err != nil {
		return rejectAnswers(block, scaffold, err)
	}

	block.Pins = characterise.Pin(scaffold, configuration, prepared.schemas, harvest)

	files := pinnedFiles(scaffold, block.Pins)
	block.Files = entriesOf(files)

	if err := verifyScaffold(ctx, runner, configuration, workRoot, prepared,
		scaffold, block.Pins, settings); err != nil {
		return rejectAnswers(block, scaffold, err)
	}

	// Promotion is what verification earns, and nothing else: an answer is
	// promoted only once the suite it produced has been proven green.
	promote(&block)

	// A characterisation whose selected rung produced no pins may never report
	// complete: green with nothing pinned is the false confidence the ladder's
	// zero-output contract exists to prevent.
	block.Complete = pinnedCount(block.Pins) > 0

	return block, files, nil
}

// openTodos counts the judgement points still awaiting an answer.
func openTodos(todos []report.Todo) int {
	open := 0

	for _, todo := range todos {
		if todo.Status == report.TodoOpen {
			open++
		}
	}

	return open
}

func pinnedCount(pins []report.Pin) int {
	count := 0

	for _, pin := range pins {
		if pin.Status == report.Pinned {
			count++
		}
	}

	return count
}

// rejectAnswers turns a failed harvest or verification into a rejected answer
// where one was supplied, and into an operational failure where none was.
//
// The distinction is the whole safety property `agent-integration.md` §2.4
// rests on: a value somebody supplied is a hypothesis the tool tests, so a
// wrong one comes back as a reported, attributed finding with the diagnostic
// attached and the artefact rewritten. A failure with no answer in play is a
// defect in the generator, and reporting that as a finding about the module
// would be the tool blaming its own bug on its user.
func rejectAnswers(
	block report.Characterisation,
	scaffold characterise.Scaffold,
	failure error,
) (report.Characterisation, []generated, error) {
	rejected := false

	for index, todo := range block.Todos {
		if todo.Status != report.TodoAnswered {
			continue
		}

		block.Todos[index].Status = report.TodoRejected
		block.Todos[index].Diagnostic = failure.Error()
		rejected = true
	}

	if !rejected {
		return report.Characterisation{}, nil, failure //nolint:exhaustruct // nothing was produced.
	}

	files := artefactFiles(scaffold, block.Todos)
	block.Pins = []report.Pin{}
	block.Files = entriesOf(files)
	block.Complete = false

	return block, files, nil
}

// artefactFiles renders the non-executable artefact for every scenario whose
// inputs are not fully resolved.
func artefactFiles(scaffold characterise.Scaffold, todos []report.Todo) []generated {
	files := make([]generated, 0, len(scaffold.Scenarios))

	for _, scenario := range scaffold.Scenarios {
		// The artefact is redacted in both views: it is the editable surface,
		// and nothing in it is ever planned.
		content := characterise.RenderArtefact(scaffold, scenario, todos)
		files = append(files, generatedFile(
			characterise.ArtefactFile(scaffold.Options.TestDirRel, scenario.Name),
			content, content, false,
		))
	}

	return files
}

// generated pairs the bytes a file is written and verified with against the
// view a report publishes.
//
// They differ only where a sensitive variable is assigned: Terraform cannot
// plan a redaction marker, and a report may not carry a secret. The digest is
// the written bytes', because that is what the write protocol commits against.
type generated struct {
	entry report.GeneratedFile
	bytes []byte
}

// generatedFile is the one place a generated file's report entry is built, so
// its digest is always the digest of the bytes that will be written.
func generatedFile(path string, written, reported []byte, executable bool) generated {
	return generated{
		entry: report.GeneratedFile{
			Path:       path,
			Content:    string(reported),
			Digest:     characterise.Digest(written),
			Executable: executable,
			Written:    false,
		},
		bytes: written,
	}
}

// entriesOf is the reported view of a generated file set.
func entriesOf(files []generated) []report.GeneratedFile {
	entries := make([]report.GeneratedFile, 0, len(files))
	for _, file := range files {
		entries = append(entries, file.entry)
	}

	return entries
}

// scaffoldArtefact renders the non-executable file the scaffolds live in.
func scaffoldArtefact(
	scaffold characterise.Scaffold,
	block *report.Characterisation,
) []generated {
	// A promoted scaffold has left the artefact: it is test content now.
	outstanding := []report.Scaffold{}

	for _, entry := range block.Scaffolds {
		if entry.Status == report.Scaffolded {
			outstanding = append(outstanding, entry)
		}
	}

	if len(outstanding) == 0 {
		return nil
	}

	content := characterise.RenderScaffolds(scaffold, outstanding)

	return []generated{generatedFile(
		characterise.ArtefactFile(scaffold.Options.TestDirRel, scaffoldScenario),
		content, content, false,
	)}
}

// pinnedFiles renders the executable suite.
func pinnedFiles(scaffold characterise.Scaffold, pins []report.Pin) []generated {
	files := make([]generated, 0, len(scaffold.Scenarios))

	for _, scenario := range scaffold.Scenarios {
		one := []report.Scenario{scenario}
		files = append(files, generatedFile(scenario.File,
			characterise.Render(scaffold, one, pins, characterise.Executable),
			characterise.Render(scaffold, one, pins, characterise.Redacted), true))
	}

	return files
}

// harvestScaffold runs the assertion-less scaffold twice and projects what it
// observed.
//
// Twice, because the difference between two runs of the same configuration is
// the only evidence that separates a value the module computes from one a mock
// invented — and pinning the second kind would generate a suite that is flaky
// by construction.
func harvestScaffold(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	workRoot string,
	prepared warm,
	scaffold characterise.Scaffold,
	settings Config,
) (characterise.Harvest, error) {
	staged := stagedScaffold(configuration, scaffold, nil, settings)

	first, err := stagedRun(ctx, runner, configuration, workRoot, prepared, staged, "harvest-1")
	if err != nil {
		return characterise.Harvest{}, err
	}

	second, err := stagedRun(ctx, runner, configuration, workRoot, prepared, staged, "harvest-2")
	if err != nil {
		return characterise.Harvest{}, err
	}

	firstPayloads, err := fingerprint.Canonicalise(first.Payloads)
	if err != nil {
		return characterise.Harvest{}, err
	}

	secondPayloads, err := fingerprint.Canonicalise(second.Payloads)
	if err != nil {
		return characterise.Harvest{}, err
	}

	return characterise.Harvest{
		Payloads: firstPayloads,
		Mask: fingerprint.Derive(firstPayloads, secondPayloads).
			Merge(staticMask(configuration.ScanVolatility(prepared.sources), firstPayloads)),
	}, nil
}

// stagedScaffold renders the overlay the sandbox materialises.
//
// One file per scenario is the naming contract. The shared-file orders are the
// scaffold-soundness gate's, and they exist because file order is the one
// thing that could make a generated scenario observe another scenario's state:
// the pins have to be identical under both, which is what the distinct state
// keys buy.
func stagedScaffold(
	configuration discovery.Configuration,
	scaffold characterise.Scaffold,
	pins []report.Pin,
	settings Config,
) map[string][]byte {
	staged := map[string][]byte{}

	if settings.SeedSharedFileOrder == "" {
		for _, scenario := range scaffold.Scenarios {
			staged[stagedPath(configuration, scenario.File)] = characterise.Render(
				scaffold, []report.Scenario{scenario}, pins, characterise.Executable,
			)
		}

		return staged
	}

	ordered := slices.Clone(scaffold.Scenarios)
	if settings.SeedSharedFileOrder == "reverse" {
		slices.Reverse(ordered)
	}

	staged[stagedPath(configuration, characterise.ScenarioFile(
		configuration.TestDirRelative(), "shared",
	))] = characterise.Render(scaffold, ordered, pins, characterise.Executable)

	return staged
}

// verifyScaffold proves the pinned suite passes before anything is written.
func verifyScaffold(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	workRoot string,
	prepared warm,
	scaffold characterise.Scaffold,
	pins []report.Pin,
	settings Config,
) error {
	staged := stagedScaffold(configuration, scaffold, pins, settings)

	result, err := stagedRun(ctx, runner, configuration, workRoot, prepared, staged, "verify")
	if err != nil {
		return err
	}

	if failures := result.FailedRuns(); len(failures) > 0 {
		return fmt.Errorf("%w: %s\n  The pins were harvested from this module's own output, "+
			"so a red scaffold is a generator defect",
			ErrScaffoldRed, describeFailures(failures, result.Diagnostics))
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("%w: terraform test exited %d\n%s",
			ErrScaffoldRed, result.ExitCode, describeDiagnostics(result.Diagnostics))
	}

	if result.ExecutedRuns() == 0 {
		return fmt.Errorf("%w: the generated suite executed no run blocks", ErrScaffoldRed)
	}

	return nil
}

// stagedRun executes the suite with the staged overlay in place: the generated
// files exist in the sandbox and in no source tree.
func stagedRun(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	workRoot string,
	prepared warm,
	staged map[string][]byte,
	name string,
) (tfexec.TestResult, error) {
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: configuration.ClosureRoot,
		ModuleRel:  configuration.RootRelative(),
		Target:     filepath.Join(workRoot, name),
		Mutations:  nil,
		Staged:     staged,
		Share:      &sandbox.Share{DataDir: prepared.dataDir, LockFile: prepared.lockFile},
		Hardlink:   true,
	})
	if err != nil {
		return tfexec.TestResult{}, err //nolint:exhaustruct // nothing ran.
	}

	return runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       true,
		Timeout:       0,
	})
}

// stagedPath converts a module-relative generated path into the
// closure-relative path the sandbox overlay is keyed by.
func stagedPath(configuration discovery.Configuration, moduleRelative string) string {
	root := configuration.RootRelative()
	if root == "." {
		return moduleRelative
	}

	return root + "/" + moduleRelative
}

// checkStagedSafety applies both safety gates to the effective staged suite.
//
// The program under judgement is the module sources plus the planned scaffold,
// because the unscaffolded module is not the thing that would execute — and
// judging it would refuse exactly the untested modules characterisation exists
// for. The provider gate requires a planned mock for every provider
// *configuration*: Terraform matches mocks to configurations by alias, so one
// mock per requirement leaves every alias reaching a real provider.
func checkStagedSafety(
	configuration discovery.Configuration,
	planned []discovery.ProviderAlias,
	settings Config,
) ([]string, error) {
	warnings, err := floorOf(configuration).checkFloor(settings)
	if err != nil {
		return nil, err
	}

	if unmocked := unmockedConfigurations(configuration, planned); len(unmocked) > 0 {
		if !settings.AllowRealInfrastructure {
			return nil, fmt.Errorf(
				"%w: the staged suite plans no mock for provider configuration %s.%s\n"+
					"  Terraform matches mocks to configurations by alias, so this "+
					"configuration would reach a real provider.\n"+
					"  Pass --allow-real-infrastructure to proceed anyway",
				ErrRealInfrastructure, strings.Join(unmocked, ", "),
				describeProviderUsers(configuration, providersOf(unmocked)),
			)
		}

		warnings = append(warnings, "the staged suite plans no mock for provider "+
			"configuration(s) "+strings.Join(unmocked, ", "))
	}

	// The effects gate is unchanged and unfooled: mocking severs a provider,
	// never a provisioner or an unsandboxed data source, and every generated
	// scenario runs in apply mode.
	effects := configuration.Effects()
	if len(effects) == 0 {
		return warnings, nil
	}

	if !settings.AllowUnsandboxedEffects {
		return nil, fmt.Errorf("%w: %s\n"+
			"  Mocking does not sever these; the generated apply-mode scenarios execute them.\n"+
			"  Pass --allow-unsandboxed-effects to proceed anyway",
			ErrUnsandboxedEffects, describeEffects(configuration, effects))
	}

	return append(warnings, "the generated scenarios will execute "+
		strconv.Itoa(len(effects))+" unsandboxed effect(s)"), nil
}

// unmockedConfigurations lists the provider configurations the staged suite
// leaves without a mock.
func unmockedConfigurations(
	configuration discovery.Configuration,
	planned []discovery.ProviderAlias,
) []string {
	mocked := map[discovery.ProviderAlias]bool{}
	for _, declared := range planned {
		mocked[declared] = true
	}

	unmocked := []string{}

	for _, declared := range characterise.Configurations(configuration) {
		if mocked[declared] {
			continue
		}

		unmocked = append(unmocked, configurationName(declared))
	}

	slices.Sort(unmocked)

	return unmocked
}

// configurationName spells a provider configuration the way Terraform does.
func configurationName(declared discovery.ProviderAlias) string {
	if declared.Alias == "" {
		return declared.Name
	}

	return declared.Name + "." + declared.Alias
}

// providersOf reduces configuration names back to provider local names, which
// is what the block-naming helper is keyed by.
func providersOf(configurations []string) []string {
	providers := []string{}

	for _, name := range configurations {
		provider, _, _ := strings.Cut(name, ".")
		if !slices.Contains(providers, provider) {
			providers = append(providers, provider)
		}
	}

	return providers
}

// seedNoEscalation puts the ladder back where the caller asked for it, so the
// zero-output contract's second half can be proven on its own. It is a seam
// control and not a command-line flag.
func seedNoEscalation(scaffold characterise.Scaffold, settings Config) characterise.Scaffold {
	if !settings.SeedNoEscalation {
		return scaffold
	}

	scaffold.Rung = scaffold.Requested
	scaffold.Escalated = false
	scaffold.EscalationReason = ""

	return scaffold
}

// seedMissingMock removes one planned provider configuration, so the staged
// provider gate can be proven to refuse before execution. It is a seam control
// and not a command-line flag.
func seedMissingMock(planned []discovery.ProviderAlias, settings Config) []discovery.ProviderAlias {
	if settings.SeedMissingMock == "" {
		return planned
	}

	kept := make([]discovery.ProviderAlias, 0, len(planned))

	for _, declared := range planned {
		if configurationName(declared) == settings.SeedMissingMock {
			continue
		}

		kept = append(kept, declared)
	}

	return kept
}
