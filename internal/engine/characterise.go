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

// characteriseStage is what every staged run needs: the runner every staged
// round executes through, the planned suite the rounds render and grade, the
// answers in force, the closure to materialise, the settings that shape it,
// the warm workspace it borrows from, and the root to build under.
//
// One value rather than a positional list: these travelled together through
// the staged pipeline, and threading them past each other is what pushed the
// loop's signatures over the argument limit.
type characteriseStage struct {
	configuration discovery.Configuration
	settings      settings
	prepared      warmWorkspace
	workRoot      string
	// terraform is the version gate's result, carried so that a staged round
	// can drive the mutation pipeline without checking the binary again.
	terraform tfexec.Version
	collector *runCollector
	// runner executes every staged round: the harvest runs, the verifications
	// and the until-dry loop's grading rounds all go through it.
	runner tfexec.Runner
	// scaffold is the planned suite every staged round renders and grades.
	scaffold characterise.SuitePlan
	// answers are the caller's judgement-point answers in force.
	answers map[string]string
	// scaffolds is the gap-closing loop's record of the constructs it
	// discovered. A path that does not run the loop leaves it empty.
	scaffolds *scaffoldSet
	// seedInitialPinDefect and seedFinalPinDefect are the pin-defect hooks
	// beside the initial and final verifiers, copied from the request.
	// Production never writes them, so a staged run's pin sets pass through
	// untouched; a nil hook is the identity.
	seedInitialPinDefect func([]characterise.Pin) []characterise.Pin
	seedFinalPinDefect   func([]characterise.Pin) []characterise.Pin
}

// initialPins passes the harvested pin set through the initial pin-defect
// hook. A nil hook is the production path and the identity: the request's
// zero value carries none, and export_test.go is the only writer of one.
func (s characteriseStage) initialPins(pins []characterise.Pin) []characterise.Pin {
	if s.seedInitialPinDefect == nil {
		return pins
	}

	return s.seedInitialPinDefect(pins)
}

// finalPins passes the pin set the loop ended with through the final
// pin-defect hook, under the same nil-is-identity rule as initialPins.
func (s characteriseStage) finalPins(pins []characterise.Pin) []characterise.Pin {
	if s.seedFinalPinDefect == nil {
		return pins
	}

	return s.seedFinalPinDefect(pins)
}

// ErrScaffoldRed reports a generated suite that did not pass in the sandbox.
//
// It is an operational failure and never a result: the whole point of
// verifying before writing is that a suite this tool puts on disk is green by
// construction, so a red scaffold is a defect in the generator rather than a
// finding about the module.
var ErrScaffoldRed = errors.New("the generated suite is not green")

// ErrWriteRefused reports a write the protocol would not perform.
var ErrWriteRefused = errors.New("refusing to write the generated suite")

// seedMissingMock is an inert test hook beside the staged provider gate it
// drives. Tests replace it with the rendered-mock removal needed to prove the
// gate refuses before execution.
//
//nolint:gochecknoglobals // test seam, inert outside the suite.
var seedMissingMock = func(_ discovery.Configuration,
	staged characterise.SuitePlan,
) characterise.SuitePlan {
	return staged
}

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
	settings settings, version tfexec.Version, collector *runCollector, hooks pinDefectHooks,
) (report.Report, error) {
	rung, answers, err := characteriseInputs(configuration, settings)
	if err != nil {
		return report.Report{}, err
	}

	// The gates first, and nothing before them but the version check.
	//
	// The program they judge is the module sources plus the effective staged
	// suite, and the mock half of that suite needs no provider schema: a
	// `mock_provider` block's name and alias come from discovery, and only the
	// pinned defaults inside it come from a schema. So the mocks are rendered
	// here, gated here, and enriched with defaults after the warm-up — and a
	// refusal costs no `init`, no provider download and no schema read,
	// exactly as it does for a mutation run.
	mockOptions := characterise.Options{TestDirRel: configuration.TestDirRelative()}
	gated := characterise.Plan(configuration, tfexec.Schemas{}, mockOptions,
		characterise.Configurations(configuration))

	warnings, err := checkStagedSafety(configuration,
		seedMissingMock(configuration, gated), settings)
	if err != nil {
		return report.Report{}, err
	}

	workRoot, err := os.MkdirTemp(settings.WorkDir, "tf-mut-")
	if err != nil {
		return report.Report{}, fmt.Errorf("creating work directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(workRoot) }()

	// Characterisation drops the collector's report record, but shares its tracer
	// with warm-up and any staged grading rounds.
	prepared, err := warmUp(ctx, runner, configuration, workRoot, collector, settings)
	if err != nil {
		return report.Report{}, err
	}

	scaffold, err := planScaffold(configuration, settings, prepared, rung, answers)
	if err != nil {
		return report.Report{}, err
	}

	warnings = append(warnings, prepared.warnings...)
	result := reportShell(configuration, settings, version.Terraform, configuration.ModuleDir, prepared, warnings)
	result.Command = report.CommandCharacterise
	// A characterisation reads the whole module: there is no count lever it
	// could have been scoped by, and saying so keeps the population contract
	// the same shape for every command.
	result.Selection = report.Selection{Mode: scopeLabel(true), Ref: "", ForcedFull: ""}

	stage := characteriseStage{
		configuration: configuration, settings: settings,
		prepared: prepared, workRoot: workRoot, terraform: version, collector: collector,
		runner: runner, scaffold: scaffold, answers: answers, scaffolds: newScaffoldSet(),
		seedInitialPinDefect: hooks.initial, seedFinalPinDefect: hooks.final,
	}

	events := dispatcher{}
	projectSuite(&events, &result)
	var block report.Characterisation
	pins, files, err := scaffoldSuite(ctx, stage, &block)
	if err != nil {
		return report.Report{}, err
	}

	// The until-dry loop grades what the scaffold pinned and pins whatever its
	// survivors still yield, over the staged suite: nothing on disk changes
	// until the caller asks for a write.
	// Gated on the judgement points, not on `Complete`. `Complete` is false for
	// a scaffold that pinned nothing, and a zero-assertion scaffold is exactly
	// the case issue #76's assertion-kills-only rule is about: the loop has to
	// run over it and report what a suite that asserts nothing actually grades,
	// rather than declining to produce a mutation population or any convergence
	// evidence for it. What the loop genuinely cannot run over is a scaffold
	// with an unresolved judgement point, because there is no executable suite.
	if settings.UntilDry && unresolvedTodos(block.Todos) == 0 {
		closed, refused, err := closeTheGap(ctx, stage, &block, pins)
		if err != nil {
			return report.Report{}, err
		}

		result.Warnings = append(result.Warnings, refused...)

		if !block.Complete {
			// The loop was refused. `refused` is one of three published stop
			// reasons, so the report that records it reaches the caller rather
			// than being discarded with an error.
			if err := events.suiteCharacterised.publish(SuiteCharacterised{suite: block}); err != nil {
				return report.Report{}, err
			}

			return result, nil
		}

		files = closed
		block.Files = entriesOf(files)
	}

	if err := ctx.Err(); err != nil {
		return report.Report{}, err
	}

	if err := commit(stage, &block, files, &result); err != nil {
		return report.Report{}, err
	}

	if err := events.suiteCharacterised.publish(SuiteCharacterised{suite: block}); err != nil {
		return report.Report{}, err
	}

	return result, nil
}

// projectSuite subscribes the report projection to the concluded suite:
// whatever the suite's outcome, the report carries it, with the metrics of an
// empty mutation population.
func projectSuite(events *dispatcher, result *report.Report) {
	events.suiteCharacterised.subscribe(func(event SuiteCharacterised) error {
		result.Characterisation = &event.suite
		result.Metrics = emptyMetrics()

		return nil
	})
}

// planScaffold plans the scaffold over the warmed-up schemas and the
// declaration sources a judgement point quotes verbatim.
func planScaffold(
	configuration discovery.Configuration,
	settings settings, prepared warmWorkspace,
	rung characterise.Rung,
	answers map[string]string,
) (characterise.SuitePlan, error) {
	sources, err := declarationSources(configuration)
	if err != nil {
		return characterise.SuitePlan{}, err
	}

	return seedNoEscalation(configuration, characterise.Plan(configuration, prepared.schemas,
		characterise.Options{
			Rung:       rung,
			TestDirRel: configuration.TestDirRelative(),
			Version:    settings.toolVersion(),
			Sources:    sources,
			Answers:    answers,
		}, characterise.Configurations(configuration))), nil
}

// seedNoEscalation is an inert test hook beside the escalation it suppresses.
// Tests replace it to prove that a rung which pins nothing is never complete.
//
//nolint:gochecknoglobals // test seam, inert outside the suite.
var seedNoEscalation = func(_ discovery.Configuration,
	scaffold characterise.SuitePlan,
) characterise.SuitePlan {
	return scaffold
}

// commit performs the write, where one was asked for, and keeps the report
// when the write left a partial state behind.
//
// A refusal before any rename leaves nothing on disk and is an error. A
// failure after the first rename has changed the caller's tree, and an error
// alone would not say which files moved.
func commit(
	stage characteriseStage,
	block *report.Characterisation,
	files []writtenFile,
	result *report.Report,
) error {
	if !stage.settings.CharacteriseWrite {
		return nil
	}

	err := commitScaffold(stage.configuration, stage.settings, stage.prepared, block, files)
	if err == nil {
		return nil
	}

	if block.Write == nil || len(block.Write.Partial) == 0 {
		return err
	}

	result.Warnings = append(result.Warnings, err.Error())

	return nil
}

// characteriseInputs resolves the two caller choices every characterisation
// starts from: the granularity, and the answers in force.
func characteriseInputs(
	configuration discovery.Configuration,
	settings settings,
) (characterise.Rung, map[string]string, error) {
	rung, err := characterise.ParseRung(settings.PinRung)
	if err != nil {
		return "", nil, err
	}

	answers, err := collectAnswers(configuration, settings)
	if err != nil {
		return "", nil, err
	}

	return rung, answers, nil
}

// refusalOrFailure separates the until-dry loop's reported refusal from its
// operational failures, and owns the refusal's whole downgrade: the report is
// kept, marked incomplete, and the failure travels as a warning on it.
//
// A refused loop is a *reported* outcome and not a swallowed error: the
// stop reason it records is one of three published values, so the report
// carrying it has to reach a reporter.
//
// Only a refusal, though. A staged suite that went red is a statement
// about the program under characterisation and belongs on the report; a
// Terraform crash, a staging failure or a mutation-engine failure is an
// operational failure, and turning one into a warning plus an incomplete
// report exits 1 where the published contract says 2.
func refusalOrFailure(block *report.Characterisation, err error) ([]string, error) {
	if !errors.Is(err, ErrBaselineRed) {
		return nil, err
	}

	block.Complete = false

	return []string{err.Error()}, nil
}

// closeTheGap runs the until-dry loop, promotes what the answers earned, and
// proves the pin set the loop ended with before any of it can be written.
//
// The final verification is the point. Each round proves the previous round's
// pins by baselining them at its start, so the last round's pins are unproven
// whenever the loop stopped because it ran out of rounds rather than because
// it went dry — and "the pinned suite is proven green before a byte is
// written" has to hold on both exits. An individually verified suggestion is
// evidence, not the same claim.
func closeTheGap(
	ctx context.Context,
	stage characteriseStage,
	block *report.Characterisation,
	pins []characterise.Pin,
) ([]writtenFile, []string, error) {
	updated, err := untilDry(ctx, stage, block, pins)
	if err != nil {
		warnings, failure := refusalOrFailure(block, err)
		if failure != nil {
			return nil, nil, failure
		}

		return nil, warnings, nil
	}

	promoted, refusals, err := promoteScaffolds(ctx, stage, block)
	if err != nil {
		return nil, nil, err
	}

	// The final leg's evidence is not carried further: the judgement points
	// and scaffolds were promoted on the legs above, and this leg re-proves
	// the pin set the loop ended with.
	pins = stage.finalPins(updated)
	block.Pins = projectPins(pins)

	if _, err := verifyScaffold(ctx, stage, pins, "verify-final"); err != nil {
		return nil, nil, err
	}

	return append(append(pinnedFiles(stage.scaffold, stage.scaffold.Scenarios, pins),
		promoted...),
		scaffoldArtefact(stage.scaffold, stage.scaffolds)...), refusals, nil
}

// scaffoldSuite harvests, pins and verifies the planned scaffold, filling the
// report block stage by stage and returning the context pins everything
// downstream — the renderer, the verifier, the until-dry loop — continues
// from.
func scaffoldSuite(
	ctx context.Context,
	stage characteriseStage,
	block *report.Characterisation,
) ([]characterise.Pin, []writtenFile, error) {
	scaffold := stage.scaffold
	*block = report.Characterisation{ // filled in below, stage by stage.
		Rung: string(scaffold.Rung), Complete: false,
		Scenarios: projectScenarios(scaffold.Scenarios), Pins: []report.Pin{},
		Todos: projectTodos(scaffold.Todos),
		Files: []report.GeneratedFile{}, Staged: !stage.settings.CharacteriseWrite,
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
		files := artefactFiles(scaffold, scaffold.Scenarios, scaffold.Todos)
		block.Files = entriesOf(files)

		return nil, files, nil
	}

	harvest, err := harvestScaffold(ctx, stage)
	if err != nil {
		rejected, failure := rejectAnswers(*block, scaffold, err)
		if failure != nil {
			return nil, nil, failure
		}

		*block = rejected.block

		return nil, rejected.files, nil
	}

	// The pins the context harvested are what everything downstream consumes:
	// the renderer, the verifier and the defect-seeding hooks all read the
	// context values, and the report block carries their projection.
	pins := stage.initialPins(
		characterise.PinHarvest(scaffold, stage.configuration, stage.prepared.schemas, harvest),
	)
	block.Pins = projectPins(pins)

	files := pinnedFiles(scaffold, scaffold.Scenarios, pins)
	block.Files = entriesOf(files)

	verification, err := verifyScaffold(ctx, stage, pins, "verify")
	if err != nil {
		rejected, failure := rejectAnswers(*block, scaffold, err)
		if failure != nil {
			return nil, nil, failure
		}

		*block = rejected.block

		return nil, rejected.files, nil
	}

	// Promotion is what verification earns, and nothing else: an answer is
	// promoted only once the suite it produced has been proven green, and the
	// transition that records it accepts only the verification's evidence.
	block.Todos = projectTodos(promoted(scaffold, verification))

	// A characterisation whose selected rung produced no pins may never report
	// complete: green with nothing pinned is the false confidence the ladder's
	// zero-output contract exists to prevent.
	block.Complete = pinnedCount(block.Pins) > 0

	return pins, files, nil
}

// openTodos counts the judgement points still awaiting an answer.
func openTodos(todos []characterise.Todo) int {
	open := 0

	for _, todo := range todos {
		if todo.Status() == characterise.TodoOpen {
			open++
		}
	}

	return open
}

// unresolvedTodos counts the judgement points that still stand between the
// scaffold and an executable suite: one nobody has answered, and one whose
// answer did not survive verification.
func unresolvedTodos(todos []report.Todo) int {
	unresolved := 0

	for _, todo := range todos {
		if todo.Status == report.TodoOpen || todo.Status == report.TodoRejected {
			unresolved++
		}
	}

	return unresolved
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

// rejectedAnswers is the outcome of an answer that did not survive: the
// report block rewritten with the judgement point rejected, and the artefact
// files that carry it. The zero value, with its error, is the "nothing was
// produced" outcome.
type rejectedAnswers struct {
	block report.Characterisation
	files []writtenFile
}

// rejectAnswers turns a failed harvest or verification into a rejected answer
// where one was supplied, and into an operational failure where none was.
//
// The distinction is the whole safety property `agent-integration.md` §2.4
// rests on: a value somebody supplied is a hypothesis the tool tests, so one
// that does not survive comes back as a reported finding with the diagnostic
// attached and the artefact rewritten. A failure with no answer in play is a
// defect in the generator, and reporting that as a finding about the module
// would be the tool blaming its own bug on its user.
//
// `rejected` means *not proven*, and deliberately not *proven wrong*. The
// suite is one program: a run that fails with an answer in play may have
// failed for a reason that has nothing to do with the answer, and no
// attribution short of re-running each answer alone could tell the two apart.
// Rejecting is the safe direction either way — promotion is earned by a green
// run and this run was not green — and the diagnostic travels verbatim so the
// reader can see what actually failed rather than take the status's word for
// it.
func rejectAnswers(
	block report.Characterisation,
	scaffold characterise.SuitePlan,
	failure error,
) (rejectedAnswers, error) {
	// Only a red suite is evidence about an answer. `harvestScaffold` and
	// `verifyScaffold` also propagate staging errors, runner errors and
	// canonicalisation errors, and none of those says anything about the value
	// somebody supplied: a Terraform crash during a run that happens to carry
	// an answer is the same operational failure it would be with no answer in
	// play, and reporting it as "your answer was refuted" both misattributes
	// the cause and swallows an exit-2 condition into an exit-1 report.
	if !errors.Is(failure, ErrScaffoldRed) {
		return rejectedAnswers{}, failure // nothing was produced.
	}

	reason := "the suite did not pass with this answer in play, so it could not be promoted; " +
		"the failure may or may not be attributable to it: " + failure.Error()

	rejected := false
	todos := make([]characterise.Todo, len(scaffold.Todos))

	for index, todo := range scaffold.Todos {
		handle, answered := scaffold.Answered[todo.ID()]
		if todo.Status() != characterise.TodoAnswered || !answered {
			todos[index] = todo

			continue
		}

		// The reason travels verbatim through the transition; the doc comment
		// above owns why rejection is the safe direction.
		todos[index] = handle.Reject(reason)
		rejected = true
	}

	if !rejected {
		return rejectedAnswers{}, failure // nothing was produced.
	}

	block.Todos = projectTodos(todos)
	files := artefactFiles(scaffold, scaffold.Scenarios, todos)
	block.Pins = []report.Pin{}
	block.Files = entriesOf(files)
	block.Complete = false

	return rejectedAnswers{block: block, files: files}, nil
}

// artefactFiles renders the non-executable artefact for every scenario whose
// inputs are not fully resolved.
func artefactFiles(
	scaffold characterise.SuitePlan,
	scenarios []characterise.Scenario,
	todos []characterise.Todo,
) []writtenFile {
	files := make([]writtenFile, 0, len(scenarios))

	for _, scenario := range scenarios {
		// The artefact is redacted in both views: it is the editable surface,
		// and nothing in it is ever planned.
		content := characterise.RenderArtefact(scaffold, scenario, todos)
		files = append(files, generatedFile(
			characterise.ArtefactFile(scaffold.Options.TestDirRel, scenario.Name()),
			content, content, false,
		))
	}

	return files
}

// writtenFile pairs the bytes a file is written and verified with against the
// view a report publishes.
//
// They differ only where a sensitive variable is assigned: Terraform cannot
// plan a redaction marker, and a report may not carry a secret. The digest is
// the written bytes', because that is what the write protocol commits against.
type writtenFile struct {
	entry report.GeneratedFile
	bytes []byte
	// digest is the written bytes' digest, which the provenance registry
	// records and the collision protocol compares against what is on disk. It
	// is deliberately not published: see generatedFile.
	digest string
}

// generatedFile is the one place a generated file's report entry is built.
//
// The published digest covers the *reported* bytes, and the written bytes'
// digest stays in the local write protocol. Publishing the executable digest
// beside the redacted content made the report an offline equality oracle for
// the secret it withheld: the template is deterministic, so a reader could
// substitute candidate values into it and compare hashes until one matched.
// The mandatory fixture's answer space is thirty-two bits. A report field that
// varies with a value the report exists not to carry is a disclosure whatever
// else it is useful for.
func generatedFile(path string, written, reported []byte, executable bool) writtenFile {
	return writtenFile{
		entry: report.GeneratedFile{
			Path:       path,
			Content:    string(reported),
			Digest:     characterise.Digest(reported),
			Executable: executable,
			Written:    false,
		},
		bytes:  written,
		digest: characterise.Digest(written),
	}
}

// entriesOf is the reported view of a generated file set.
func entriesOf(files []writtenFile) []report.GeneratedFile {
	entries := make([]report.GeneratedFile, 0, len(files))
	for _, file := range files {
		entries = append(entries, file.entry)
	}

	return entries
}

// scaffoldArtefact renders the non-executable file the scaffolds live in.
func scaffoldArtefact(
	scaffold characterise.SuitePlan,
	scaffolds *scaffoldSet,
) []writtenFile {
	// A promoted scaffold has left the artefact: it is test content now.
	outstanding := scaffolds.scaffolded()
	if len(outstanding) == 0 {
		return nil
	}

	content := characterise.RenderScaffolds(scaffold, outstanding)

	return []writtenFile{generatedFile(
		characterise.ArtefactFile(scaffold.Options.TestDirRel, scaffoldScenario),
		content, content, false,
	)}
}

// pinnedFiles renders the executable suite.
func pinnedFiles(
	scaffold characterise.SuitePlan,
	scenarios []characterise.Scenario,
	pins []characterise.Pin,
) []writtenFile {
	files := make([]writtenFile, 0, len(scenarios))

	for _, scenario := range scenarios {
		one := []characterise.Scenario{scenario}
		files = append(files, generatedFile(scenario.File(),
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
	stage characteriseStage,
) (characterise.Harvest, error) {
	staged := stagedScaffold(stage.configuration, stage.scaffold, nil)

	first, err := stagedRun(ctx, stage, staged, "harvest-1")
	if err != nil {
		return characterise.Harvest{}, err
	}

	second, err := stagedRun(ctx, stage, staged, "harvest-2")
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
			Merge(staticMask(
				stage.configuration.ScanVolatility(stage.prepared.sources), firstPayloads,
			)),
	}, nil
}

// seedSharedFileOrder exposes shared-file staging only to test order invariance.
//
//nolint:gochecknoglobals // inert test hook; external tests own it sequentially.
var seedSharedFileOrder = func(discovery.Configuration) string { return "" }

// stagedScaffold renders the overlay the sandbox materialises.
//
// One file per scenario is the naming contract. The shared-file orders are the
// scaffold-soundness gate's, and they exist because file order is the one
// thing that could make a generated scenario observe another scenario's state:
// the pins have to be identical under both, which is what the distinct state
// keys buy.
func stagedScaffold(
	configuration discovery.Configuration,
	scaffold characterise.SuitePlan,
	pins []characterise.Pin,
) map[string][]byte {
	staged := map[string][]byte{}
	sharedFileOrder := seedSharedFileOrder(configuration)

	if sharedFileOrder == "" {
		for _, scenario := range scaffold.Scenarios {
			staged[stagedPath(configuration, scenario.File())] = characterise.Render(
				scaffold, []characterise.Scenario{scenario}, pins, characterise.Executable,
			)
		}

		return staged
	}

	// Cloned, not aliased: the order is the staged suite's, and reversing the
	// plan's own slice would reorder the harvest points the report publishes.
	ordered := slices.Clone(scaffold.Scenarios)
	if sharedFileOrder == "reverse" {
		slices.Reverse(ordered)
	}

	staged[stagedPath(configuration, characterise.ScenarioFile(
		configuration.TestDirRelative(), "shared",
	))] = characterise.Render(scaffold, ordered, pins, characterise.Executable)

	return staged
}

// verifyScaffold proves the pinned suite passes before anything is written.
// On success it returns the evidence that the verification passed, which is
// what a promotion transition demands.
func verifyScaffold(
	ctx context.Context,
	stage characteriseStage,
	pins []characterise.Pin,
	name string,
) (characterise.Verification, error) {
	staged := stagedScaffold(stage.configuration, stage.scaffold, pins)

	result, err := stagedRun(ctx, stage, staged, name)
	if err != nil {
		return characterise.Verification{}, err
	}

	if failures := result.FailedRuns(); len(failures) > 0 {
		return characterise.Verification{}, fmt.Errorf("%w: %s\n  The pins were harvested from this module's own output, "+
			"so a red scaffold is a generator defect",
			ErrScaffoldRed, describeFailures(failures, result.Diagnostics))
	}

	if result.ExitCode != 0 {
		return characterise.Verification{}, fmt.Errorf("%w: terraform test exited %d\n%s",
			ErrScaffoldRed, result.ExitCode, describeDiagnostics(result.Diagnostics))
	}

	if result.ExecutedRuns() == 0 {
		return characterise.Verification{},
			fmt.Errorf("%w: the generated suite executed no run blocks", ErrScaffoldRed)
	}

	return characterise.Verified(), nil
}

// stagedRun executes the suite with the staged overlay in place: the generated
// files exist in the sandbox and in no source tree.
//
// Staged runs are infrastructure, not mutants under judgement, and at harvest
// time no baseline exists to scale a measured budget from — so they carry the
// absolute preflight cap. A staged run that outlives it ends the command:
// whatever the harvest or the verification round caught before the deadline
// is not evidence a pin may be made from.
func stagedRun(
	ctx context.Context,
	stage characteriseStage,
	staged map[string][]byte,
	name string,
) (tfexec.TestResult, error) {
	built, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: stage.configuration.ClosureRoot,
		ModuleRel:  stage.configuration.RootRelative(),
		Target:     filepath.Join(stage.workRoot, name),
		Mutations:  nil,
		Staged:     staged,
		Share: &sandbox.Share{
			DataDir: stage.prepared.dataDir, LockFile: stage.prepared.lockFile,
		},
		Hardlink: true,
	})
	if err != nil {
		return tfexec.TestResult{}, err // nothing ran.
	}

	budget := stage.settings.preflightCap()

	result, err := stage.runner.Test(ctx, built.ModuleDir, tfexec.TestOptions{
		TestDirectory: stage.configuration.TestDirRelative(),
		Filters:       nil,
		Verbose:       true,
		Timeout:       budget,
	})
	if result.TimedOut {
		return tfexec.TestResult{}, // the run did not complete.
			fmt.Errorf("%w: the staged run %s did not finish within %s: %w",
				tfexec.ErrNotExecuted, name, budget, context.DeadlineExceeded)
	}

	return result, err
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
// The program under judgement is the module sources plus the *rendered mocks*
// of the scaffold, because the unscaffolded module is not the thing that would
// execute — and judging it would refuse exactly the untested modules
// characterisation exists for. The provider gate requires a mock for every
// provider *configuration*: Terraform matches mocks to configurations by
// alias, so one mock per requirement leaves every alias reaching a real
// provider.
//
// Two limits, stated because this comment is what the next reader will trust.
// The gate reads the rendered mocks and not the whole rendered suite, so it
// checks the renderer against the plan rather than enumerating the
// configurations Terraform will use independently — nothing here can see a
// configuration `Configurations()` does not find. And a scenario's *answers*
// reach the staged suite without passing a gate, which is safe only because
// `characterise.Synthesise` constrains an answer to a constant expression:
// widen that grammar and this comment stops being true.
func checkStagedSafety(
	configuration discovery.Configuration,
	staged characterise.SuitePlan,
	settings settings,
) ([]string, error) {
	warnings, err := floorOf(configuration).checkFloor(settings)
	if err != nil {
		return nil, err
	}

	unmocked, err := unmockedConfigurations(configuration, staged)
	if err != nil {
		return nil, err
	}

	if len(unmocked) > 0 {
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
//
// The mocked set is read back out of the *rendered* mock blocks, not out of
// the plan that produced them. Comparing the configurations discovery found
// against the configurations something intended to mock compares a set with
// itself: no input can separate them, and the gate cannot fire. Comparing them
// against what the renderer emitted catches a scaffold that plans a mock and
// writes none, or writes it under the wrong alias.
func unmockedConfigurations(
	configuration discovery.Configuration,
	staged characterise.SuitePlan,
) ([]string, error) {
	rendered, err := discovery.MocksIn(characterise.RenderMocks(staged))
	if err != nil {
		return nil, err
	}

	mocked := map[discovery.ProviderAlias]bool{}
	for _, declared := range rendered {
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

	return unmocked, nil
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
