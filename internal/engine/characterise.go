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
	workRoot, err := os.MkdirTemp(settings.WorkDir, "tf-mut-")
	if err != nil {
		return report.Report{}, fmt.Errorf("creating work directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	prepared, err := warmUp(ctx, runner, configuration, workRoot)
	if err != nil {
		return report.Report{}, err
	}

	rung, err := characterise.ParseRung(settings.PinRung)
	if err != nil {
		return report.Report{}, err
	}

	answers, err := collectAnswers(configuration, settings)
	if err != nil {
		return report.Report{}, err
	}

	scaffold := characterise.Plan(configuration, prepared.schemas, characterise.Options{
		Rung:       rung,
		TestDirRel: configuration.TestDirRelative(),
		Version:    version.Terraform,
		Sources:    prepared.sources,
		Answers:    answers,
	})

	scaffold = seedMissingMock(seedNoEscalation(scaffold, settings), settings)

	warnings, err := checkStagedSafety(configuration, scaffold, settings)
	if err != nil {
		return report.Report{}, err
	}

	warnings = append(warnings, prepared.warnings...)

	result := shell(configuration, settings, version.Terraform,
		configuration.ModuleDir, prepared, warnings)
	result.Command = report.CommandCharacterise
	// A characterisation reads the whole module: there is no count lever it
	// could have been scoped by, and saying so keeps the population contract
	// the same shape for every command.
	result.Selection = report.Selection{Mode: scopeLabel(true), Ref: "", ForcedFull: ""}

	block, err := scaffoldSuite(ctx, runner, configuration, settings, workRoot, prepared, scaffold)
	if err != nil {
		return report.Report{}, err
	}

	result.Characterisation = &block
	result.Metrics = report.ComputeMetrics(nil)

	if settings.CharacteriseWrite {
		if err := commitScaffold(configuration, settings, prepared, &block); err != nil {
			return report.Report{}, err
		}

		result.Characterisation = &block
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
) (report.Characterisation, error) {
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
		block.Files = artefactFiles(scaffold)

		return block, nil
	}

	harvest, err := harvestScaffold(ctx, runner, configuration, workRoot, prepared, scaffold, settings)
	if err != nil {
		return report.Characterisation{}, err
	}

	block.Pins = characterise.Pin(scaffold, configuration, prepared.schemas, harvest)
	block.Files = pinnedFiles(scaffold, block.Pins)

	if err := verifyScaffold(ctx, runner, configuration, workRoot, prepared,
		scaffold, block.Pins, settings); err != nil {
		return report.Characterisation{}, err
	}

	// Promotion is what verification earns, and nothing else: an answer is
	// promoted only once the suite it produced has been proven green.
	promote(&block)

	// A characterisation whose selected rung produced no pins may never report
	// complete: green with nothing pinned is the false confidence the ladder's
	// zero-output contract exists to prevent.
	block.Complete = pinnedCount(block.Pins) > 0

	return block, nil
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

// artefactFiles renders the non-executable artefact for every scenario whose
// inputs are not fully resolved.
func artefactFiles(scaffold characterise.Scaffold) []report.GeneratedFile {
	files := make([]report.GeneratedFile, 0, len(scaffold.Scenarios))

	for _, scenario := range scaffold.Scenarios {
		content := characterise.RenderArtefact(scaffold, scenario, scaffold.Todos)
		files = append(files, report.GeneratedFile{
			Path:       characterise.ArtefactFile(scaffold.Options.TestDirRel, scenario.Name),
			Content:    string(content),
			Digest:     characterise.Digest(content),
			Executable: false,
			Written:    false,
		})
	}

	return files
}

// pinnedFiles renders the executable suite.
func pinnedFiles(scaffold characterise.Scaffold, pins []report.Pin) []report.GeneratedFile {
	files := make([]report.GeneratedFile, 0, len(scaffold.Scenarios))

	for _, scenario := range scaffold.Scenarios {
		content := characterise.Render(scaffold, []report.Scenario{scenario}, pins)
		files = append(files, report.GeneratedFile{
			Path:       scenario.File,
			Content:    string(content),
			Digest:     characterise.Digest(content),
			Executable: true,
			Written:    false,
		})
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
			staged[stagedPath(configuration, scenario.File)] = characterise.Render(scaffold, []report.Scenario{scenario}, pins)
		}

		return staged
	}

	ordered := slices.Clone(scaffold.Scenarios)
	if settings.SeedSharedFileOrder == "reverse" {
		slices.Reverse(ordered)
	}

	staged[stagedPath(configuration, characterise.ScenarioFile(
		configuration.TestDirRelative(), "shared",
	))] = characterise.Render(scaffold, ordered, pins)

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
	scaffold characterise.Scaffold,
	settings Config,
) ([]string, error) {
	warnings, err := floorOf(configuration).checkFloor(settings)
	if err != nil {
		return nil, err
	}

	if unmocked := unmockedConfigurations(configuration, scaffold); len(unmocked) > 0 {
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
	scaffold characterise.Scaffold,
) []string {
	planned := map[discovery.ProviderAlias]bool{}
	for _, mock := range scaffold.Mocks {
		planned[discovery.ProviderAlias{Name: mock.Name, Alias: mock.Alias}] = true
	}

	unmocked := []string{}

	for _, declared := range characterise.Configurations(configuration) {
		if planned[declared] {
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

// seedMissingMock removes one planned alias mock, so the staged provider gate
// can be proven to refuse before execution. It is a seam control and not a
// command-line flag.
func seedMissingMock(scaffold characterise.Scaffold, settings Config) characterise.Scaffold {
	if settings.SeedMissingMock == "" {
		return scaffold
	}

	kept := make([]characterise.Mock, 0, len(scaffold.Mocks))

	for _, mock := range scaffold.Mocks {
		if configurationName(discovery.ProviderAlias{Name: mock.Name, Alias: mock.Alias}) ==
			settings.SeedMissingMock {
			continue
		}

		kept = append(kept, mock)
	}

	scaffold.Mocks = kept

	return scaffold
}
