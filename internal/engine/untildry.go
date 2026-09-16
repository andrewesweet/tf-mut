package engine

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The until-dry loop: scaffold, mutate, pin the survivors, repeat.
//
// Two constraints from the adversarial review shape it. Completeness is
// measured in **assertion kills only** — Terraform's plan-time evaluation
// kills mutants even under an assertion-free suite, so counting errors would
// give a freshly scaffolded, zero-assertion suite a flattering score from
// iteration zero. And the loop **respects the granularity ladder**: it pins
// only survivor deltas at or below the chosen rung, because an unconstrained
// loop drives monotonically towards pinning every configured attribute, which
// is the brittle level reached without the user choosing it.
//
// The suite it iterates over is the **staged suite**: an overlay materialised
// into a staging root that discovery, mutation execution, suggestion targeting
// and verification all read, so a loop that converges without `--write` leaves
// the source tree byte-identical.

// defaultRounds bounds the loop. Each pinned assertion perturbs the kill sets
// of the others, so a fixed point is not guaranteed; the bound plus the
// convergence report is the honest answer to that.
const defaultRounds = 5

// stagingRoot is the directory the staged suite is materialised into.
const stagingRoot = "staged"

//nolint:gochecknoglobals // test seam, inert outside the suite.
var seedUntilDryRounds = func(config) int { return 0 }

// roundLimit is the loop's bound, which a seam control may lower so that the
// `bounded` exit can be staged rather than argued about.
func roundLimit(settings config) int {
	if rounds := seedUntilDryRounds(settings); rounds > 0 {
		return rounds
	}

	return defaultRounds
}

// untilDry iterates the scaffold against the mutation loop until the survivors
// stop yielding new assertions at the chosen granularity. It returns the pin
// set it ended with: each round appends through the context's constructor, and
// the caller re-projects the result onto the report.
func untilDry(
	ctx context.Context,
	runner tfexec.Runner,
	stage staging,
	block *report.Characterisation,
	scaffold characterise.SuitePlan,
	pins []characterise.Pin,
	scaffolds *scaffoldSet,
) ([]characterise.Pin, error) {
	convergence := &report.Convergence{
		Rounds: 0, NewPinsPerRound: []int{}, StopReason: "bounded",
	}
	block.Convergence = convergence

	for round := range roundLimit(stage.settings) {
		// Every round stages the suite as it stands *now*. Building the files
		// once before the loop would mean round N+1 grading round N-1's suite,
		// treating the assertions round N added as merely known, and declaring
		// the run dry without ever having executed them.
		updated, added, err := oneRound(ctx, runner, stage, block, scaffold, pins, scaffolds,
			filepath.Join(stage.workRoot, stagingRoot+"-"+strconv.Itoa(round)))
		if err != nil {
			// The stop reason is a published value of a closed vocabulary, so
			// the report that carries it has to reach a reporter. The caller
			// keeps the block and renders it; the loop's failure is a warning
			// on a report rather than an error that discards one.
			convergence.StopReason = "refused"

			return pins, err
		}

		pins = updated

		convergence.Rounds++
		convergence.NewPinsPerRound = append(convergence.NewPinsPerRound, added)

		if added == 0 {
			convergence.StopReason = "dry"

			break
		}
	}

	return pins, nil
}

// oneRound grades the staged suite and pins whatever the survivors yield.
func oneRound(
	ctx context.Context,
	runner tfexec.Runner,
	stage staging,
	block *report.Characterisation,
	scaffold characterise.SuitePlan,
	pins []characterise.Pin,
	scaffolds *scaffoldSet,
	target string,
) ([]characterise.Pin, int, error) {
	staged, err := stageSuite(stage, scaffold, pins, target)
	if err != nil {
		return pins, 0, err
	}

	graded := stage.settings
	graded.mode = suggestMode
	graded.UntilDry = false
	graded.CharacteriseWrite = false
	graded.SuggestDryRun = false
	graded.ModuleDir = staged.ModuleDir
	graded.NoCache = true

	// The staged suite lives in a temporary tree, so a verdict cache rooted at
	// it could never be reused anyway; disabling it says that outright rather
	// than leaving a cache directory in a directory that is about to vanish.
	stagedConfiguration, err := discovery.DiscoverWith(staged.ModuleDir,
		stage.settings.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(stage.settings)})
	if err != nil {
		return pins, 0, err
	}

	result, err := mutate(ctx, runner, stagedConfiguration, graded,
		stage.terraform, staged.ModuleDir)
	if err != nil {
		return pins, 0, err
	}

	recordScaffolds(block, scaffolds, result)

	// The round's survivors are evidence only where the round observed its
	// whole population: a round that timed mutants out has not shown that the
	// survivors stopped yielding, only that it stopped waiting.
	authoritative, err := newAuthoritativePopulation(result, ErrUntilDryPopulation,
		"  A round that did not observe its whole population has not shown that the\n"+
			"  survivors stopped yielding, only that it stopped waiting")
	if err != nil {
		return pins, 0, err
	}

	updated, added := absorb(block, pins, authoritative)

	return updated, added, nil
}

// promoteScaffolds verifies each answered scaffold's `expect_failures`
// behaviour and promotes only what passed.
//
// Promotion is earned, never granted. The answer supplies the inputs that make
// the construct fail; the tool stages the run block, executes it, and promotes
// only if Terraform agrees the failure happened — a run asserting a failure
// that does not occur is a failing run, which is what makes the check worth
// running at all. A scaffold nobody answered, and one whose answer did not
// produce the failure, both stay non-executable. The move itself runs through
// the context's transition — the only route that carries the verification that
// earned it — and the report carries the projection of the result.
func promoteScaffolds(
	ctx context.Context,
	runner tfexec.Runner,
	stage staging,
	block *report.Characterisation,
	scaffold characterise.SuitePlan,
	answers map[string]string,
	scaffolds *scaffoldSet,
) ([]generated, []string) {
	promoted := []generated{}
	warnings := []string{}

	for _, entry := range scaffolds.all() {
		answer, answered := answers[entry.ID()]
		if !answered {
			continue
		}

		file, refusal := verifyScaffoldAnswer(ctx, runner, stage, scaffold, entry, answer,
			filepath.Join(stage.workRoot, "scaffold-"+entry.ID()))
		if refusal != "" {
			warnings = append(warnings, "scaffold "+entry.ID()+" was not promoted: "+refusal)

			continue
		}

		scaffolds.replace(entry.Promote(characterise.Verified()))
		promoted = append(promoted, file)
	}

	block.Scaffolds = projectScaffolds(scaffolds.all())

	return promoted, warnings
}

// verifyScaffoldAnswer stages one answered scaffold and executes it.
func verifyScaffoldAnswer(
	ctx context.Context,
	runner tfexec.Runner,
	stage staging,
	scaffold characterise.SuitePlan,
	entry characterise.Scaffold,
	answer, target string,
) (generated, string) {
	empty := generated{} //nolint:exhaustruct // the not-promoted sentinel.

	checkable, addressable := characterise.Checkable(entry.Address())
	if !addressable {
		return empty, entry.Address() + " names no object expect_failures can accept"
	}

	variables, parsed := characterise.AnsweredVariables(answer)
	if !parsed {
		return empty, "the answer is not an object of constant assignments to legal " +
			"variable names"
	}

	// Every name has to be an input the module declares. A legal identifier is
	// not enough on its own: an answer naming something the module has no
	// variable for would render a run block Terraform refuses, and the point
	// of checking here is that the refusal names the answer rather than the
	// generated file.
	if undeclared := undeclaredInputs(stage.configuration, variables); undeclared != "" {
		return empty, "the answer names " + undeclared + ", which the module does not declare"
	}

	content := characterise.RenderExpectFailures(scaffold, entry, checkable, variables)
	path := characterise.ScaffoldFile(scaffold.Options.TestDirRel, entry.ID())

	scoped := stage
	scoped.workRoot = target

	result, err := stagedRun(ctx, runner, scoped,
		map[string][]byte{stagedPath(stage.configuration, path): content}, "verify")
	if err != nil {
		return empty, err.Error()
	}

	if failures := result.FailedRuns(); len(failures) > 0 || result.ExitCode != 0 {
		return empty, "the expected failure did not happen, so the check proves nothing"
	}

	return generatedFile(path, content, content, true), ""
}

// scaffoldSet is the context-side record of the scaffolds the loop discovers,
// keyed by the construct address that deduplicates them: one scaffold per
// construct, whatever produced it and whatever round found it. The report
// block carries the projection; the record carries the context values, so
// promotion runs through the context's transition and never by re-writing a
// projected status.
type scaffoldSet struct {
	byAddress map[string]characterise.Scaffold
}

func newScaffoldSet() *scaffoldSet {
	return &scaffoldSet{byAddress: map[string]characterise.Scaffold{}}
}

// record adds a scaffold unless its address is already recorded.
func (s *scaffoldSet) record(entry characterise.Scaffold) {
	if _, known := s.byAddress[entry.Address()]; known {
		return
	}

	s.byAddress[entry.Address()] = entry
}

// replace swaps a recorded scaffold for its transition result.
func (s *scaffoldSet) replace(entry characterise.Scaffold) {
	s.byAddress[entry.Address()] = entry
}

// all lists every recorded scaffold sorted by address — the order both the
// artefact and the report publish.
func (s *scaffoldSet) all() []characterise.Scaffold {
	entries := make([]characterise.Scaffold, 0, len(s.byAddress))

	for _, entry := range s.byAddress {
		entries = append(entries, entry)
	}

	slices.SortFunc(entries, func(left, right characterise.Scaffold) int {
		return strings.Compare(left.Address(), right.Address())
	})

	return entries
}

// scaffolded lists the recorded scaffolds still awaiting an answer, by address.
// A promoted scaffold has left the artefact: it is test content now.
func (s *scaffoldSet) scaffolded() []characterise.Scaffold {
	outstanding := []characterise.Scaffold{}

	for _, entry := range s.all() {
		if entry.Status() == characterise.StatusScaffolded {
			outstanding = append(outstanding, entry)
		}
	}

	return outstanding
}

// recordScaffolds turns every construct the oracle cannot assert on into a
// non-executable scaffold.
//
// This is the M4 spec review's C4 relocation: skeleton generation for
// `StructurallyUnassertable` constructs was removed from M4 because it had no
// proven delta and is unverifiable by construction, and it lands here as a
// separate artefact class. It is never `verified`, never executable, and never
// touched by `suggest --apply`: the scaffold names the construct and the shape
// of the check somebody has to write, and stays outside the suite until that
// check has been written and proven.
func recordScaffolds(block *report.Characterisation, scaffolds *scaffoldSet, result report.Report) {
	for _, mutant := range result.Mutants {
		if mutant.State != report.StructurallyUnassertable {
			continue
		}

		scaffolds.record(characterise.Scaffolded(
			mutant.Site,
			characterise.ArtefactFile(result.TestDirectory, scaffoldScenario),
		))
	}

	block.Scaffolds = projectScaffolds(scaffolds.all())
}

// scaffoldScenario names the artefact the scaffolds live in.
const scaffoldScenario = "scaffolds"

// stageSuite materialises the closure plus the current generated suite.
//
// It borrows the warm workspace the run already built, the way `stagedRun`
// does. Without that each round copied the whole closure by value and `mutate`
// re-initialised from scratch — provider install and `providers schema`, five
// times over at the default bound, against a real provider tree, inside a loop
// the shipped skill tells agents to call routinely.
func stageSuite(
	stage staging,
	scaffold characterise.SuitePlan,
	pins []characterise.Pin,
	target string,
) (sandbox.Sandbox, error) {
	// Re-rendered from the pins as they stand, not replayed from the report:
	// the report's view of a generated file is redacted, and a suite staged
	// from it would plan a redaction marker.
	staged := map[string][]byte{}

	for _, file := range pinnedFiles(scaffold, scaffold.Scenarios, pins) {
		if file.entry.Executable {
			staged[stagedPath(stage.configuration, file.entry.Path)] = file.bytes
		}
	}

	return sandbox.Materialise(sandbox.Spec{
		SourceRoot: stage.configuration.ClosureRoot,
		ModuleRel:  stage.configuration.RootRelative(),
		Target:     target,
		Mutations:  nil,
		Staged:     staged,
		Share: &sandbox.Share{
			DataDir: stage.prepared.dataDir, LockFile: stage.prepared.lockFile,
		},
		Hardlink: true,
	})
}

// absorb takes the verified suggestions the round produced and turns the ones
// at or below the chosen rung into pins.
//
// It accepts an authoritativePopulation and is reachable no other way: the
// count it returns is the convergence claim's whole evidence, and a zero
// drawn over mutants that never ran would declare the loop dry over
// survivors it never observed.
//
// Only verified suggestions: an unverified one is a candidate the tool has not
// proven kills anything, and pinning it would put an unproven assertion into a
// suite whose whole claim is that everything in it was observed. The append
// goes through the context's constructor, and the report block carries the
// projection of the set the loop now holds.
func absorb(
	block *report.Characterisation,
	pins []characterise.Pin,
	authoritative authoritativePopulation,
) ([]characterise.Pin, int) {
	// Keyed by scenario as well as expression: two scenarios legitimately need
	// the same rendered condition, and a global set would silently drop the
	// second one.
	known := map[string]bool{}
	for _, pin := range pins {
		known[pin.Scenario()+"\x00"+pin.Expression()] = true
	}

	rung := characterise.Rung(block.Rung)
	added := 0

	for _, suggestion := range authoritative.graded().Suggestions {
		scenario, found := scenarioForRun(block, suggestion.TargetRun)
		if suggestion.Status != report.SuggestionVerified || !found {
			continue
		}

		key := scenario + "\x00" + suggestion.Expression
		if known[key] {
			continue
		}

		level := rungOfExpression(suggestion.Expression)
		if !rung.Includes(level) {
			continue
		}

		known[key] = true
		added++

		address := assertedAddress(suggestion.Expression)

		pins = append(pins,
			characterise.Pinned(scenario, address, suggestion.Expression, string(level)))
	}

	slices.SortFunc(pins, func(left, right characterise.Pin) int {
		if order := strings.Compare(left.Scenario(), right.Scenario()); order != 0 {
			return order
		}

		return strings.Compare(left.Address(), right.Address())
	})

	block.Pins = projectPins(pins)

	return pins, added
}

// scenarioForRun maps a suggestion's target run back to the scenario that
// generated it. A suggestion aimed at a run block this tool did not generate
// is left alone: the loop pins its own scaffold and never edits somebody
// else's suite.
func scenarioForRun(block *report.Characterisation, run string) (string, bool) {
	for _, scenario := range block.Scenarios {
		if characterise.RunPrefix+scenario.Name == run {
			return scenario.ID, true
		}
	}

	return "", false
}

// undeclaredInputs names the first assignment whose variable the module does
// not declare, or empty where every one of them is real.
func undeclaredInputs(
	configuration discovery.Configuration,
	variables map[string]string,
) string {
	declared := map[string]bool{}

	for _, module := range configuration.Modules {
		if module.Dir != configuration.ModuleDir {
			continue
		}

		for _, variable := range module.Variables {
			declared[variable.Name] = true
		}
	}

	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		if !declared[name] {
			return name
		}
	}

	return ""
}

// rungOfExpression classifies a generated assertion by the ladder level it
// belongs to.
//
// It classifies the *subject* the assertion addresses, never the syntax it is
// rendered in. `length(...)` is not a counts-rung marker: the suggestion
// engine renders a configured collection attribute the same way, so keying off
// the call would admit configured-value assertions into a `--pin counts` run —
// which is exactly the unrequested brittleness the ladder exists to prevent.
// A `length` over a bare resource address is a count; a `length` over an
// attribute of one is that attribute's value.
func rungOfExpression(expression string) characterise.Rung {
	address := assertedAddress(expression)

	switch {
	case strings.HasPrefix(address, "output."):
		return characterise.RungOutputs
	case strings.HasPrefix(expression, lengthCall) && resourceAddressOnly(address):
		return characterise.RungCounts
	default:
		return characterise.RungConfigured
	}
}

// lengthCall is the one collection-safe form the M4 rendering contract admits.
const lengthCall = "length("

// assertedAddress reads the address a generated assertion is about: the left
// side of the equality, with any `length(...)` wrapper removed.
func assertedAddress(expression string) string {
	subject, _, found := strings.Cut(expression, " == ")
	if !found {
		subject = expression
	}

	subject = strings.TrimSpace(subject)

	if inner, wrapped := strings.CutPrefix(subject, lengthCall); wrapped {
		subject = strings.TrimSuffix(inner, ")")
	}

	return strings.TrimSpace(subject)
}

// resourceAddressOnly reports an address that names a resource collection and
// nothing inside it — `null_resource.app`, not `null_resource.app.triggers`.
func resourceAddressOnly(address string) bool {
	return len(discovery.ParseAddr(address).Parts) == collectionAddressParts
}

// collectionAddressParts is the length of `<type>.<name>`: a resource
// collection named whole, with no attribute after it.
const collectionAddressParts = 2
