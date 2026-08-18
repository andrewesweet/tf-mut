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

// untilDry iterates the scaffold against the mutation loop until the survivors
// stop yielding new assertions at the chosen granularity.
func untilDry(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	version tfexec.Version,
	block *report.Characterisation,
	scaffold characterise.Scaffold,
	workRoot string,
) error {
	convergence := &report.Convergence{
		Rounds: 0, NewPinsPerRound: []int{}, StopReason: "bounded",
	}
	block.Convergence = convergence

	for round := range defaultRounds {
		// Every round stages the suite as it stands *now*. Building the files
		// once before the loop would mean round N+1 grading round N-1's suite,
		// treating the assertions round N added as merely known, and declaring
		// the run dry without ever having executed them.
		added, err := oneRound(ctx, runner, configuration, settings, version, block, scaffold,
			filepath.Join(workRoot, stagingRoot+"-"+strconv.Itoa(round)))
		if err != nil {
			convergence.StopReason = "refused"

			return err
		}

		convergence.Rounds++
		convergence.NewPinsPerRound = append(convergence.NewPinsPerRound, added)

		if added == 0 {
			convergence.StopReason = "dry"

			break
		}
	}

	return nil
}

// oneRound grades the staged suite and pins whatever the survivors yield.
func oneRound(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	settings Config,
	version tfexec.Version,
	block *report.Characterisation,
	scaffold characterise.Scaffold,
	target string,
) (int, error) {
	staged, err := stageSuite(configuration, scaffold, block, target)
	if err != nil {
		return 0, err
	}

	graded := settings
	graded.Characterise = false
	graded.UntilDry = false
	graded.CharacteriseWrite = false
	graded.Suggest = true
	graded.SuggestDryRun = false
	graded.ModuleDir = staged.ModuleDir
	graded.NoCache = true

	// The staged suite lives in a temporary tree, so a verdict cache rooted at
	// it could never be reused anyway; disabling it says that outright rather
	// than leaving a cache directory in a directory that is about to vanish.
	stagedConfiguration, err := discovery.DiscoverWith(staged.ModuleDir,
		settings.TestDirectory, discovery.Options{SkipJSON: settings.DisableJSONReading})
	if err != nil {
		return 0, err
	}

	result, err := mutate(ctx, runner, stagedConfiguration, graded, version, staged.ModuleDir)
	if err != nil {
		return 0, err
	}

	recordScaffolds(block, result)

	return absorb(block, result), nil
}

// promoteScaffolds verifies each answered scaffold's `expect_failures`
// behaviour and promotes only what passed.
//
// Promotion is earned, never granted. The answer supplies the inputs that make
// the construct fail; the tool stages the run block, executes it, and promotes
// only if Terraform agrees the failure happened — a run asserting a failure
// that does not occur is a failing run, which is what makes the check worth
// running at all. A scaffold nobody answered, and one whose answer did not
// produce the failure, both stay non-executable.
func promoteScaffolds(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared warm,
	block *report.Characterisation,
	scaffold characterise.Scaffold,
	answers map[string]string,
	workRoot string,
) ([]generated, []string) {
	promoted := []generated{}
	warnings := []string{}

	for index, entry := range block.Scaffolds {
		answer, answered := answers[entry.ID]
		if !answered {
			continue
		}

		file, refusal := verifyScaffoldAnswer(ctx, runner, configuration, prepared,
			scaffold, entry, answer, filepath.Join(workRoot, "scaffold-"+entry.ID))
		if refusal != "" {
			warnings = append(warnings, "scaffold "+entry.ID+" was not promoted: "+refusal)

			continue
		}

		block.Scaffolds[index].Status = report.ScaffoldPromoted
		block.Scaffolds[index].Artefact = ""
		promoted = append(promoted, file)
	}

	return promoted, warnings
}

// verifyScaffoldAnswer stages one answered scaffold and executes it.
func verifyScaffoldAnswer(
	ctx context.Context,
	runner tfexec.Runner,
	configuration discovery.Configuration,
	prepared warm,
	scaffold characterise.Scaffold,
	entry report.Scaffold,
	answer, target string,
) (generated, string) {
	empty := generated{} //nolint:exhaustruct // the not-promoted sentinel.

	checkable, addressable := characterise.Checkable(entry.Address)
	if !addressable {
		return empty, entry.Address + " names no object expect_failures can accept"
	}

	variables, parsed := characterise.AnsweredVariables(answer)
	if !parsed {
		return empty, "the answer is not an object of constant input assignments"
	}

	content := characterise.RenderExpectFailures(scaffold, entry, checkable, variables)
	path := characterise.ScaffoldFile(scaffold.Options.TestDirRel, entry.ID)

	result, err := stagedRun(ctx, runner, configuration, target, prepared,
		map[string][]byte{stagedPath(configuration, path): content}, "verify")
	if err != nil {
		return empty, err.Error()
	}

	if failures := result.FailedRuns(); len(failures) > 0 || result.ExitCode != 0 {
		return empty, "the expected failure did not happen, so the check proves nothing"
	}

	return generatedFile(path, content, content, true), ""
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
func recordScaffolds(block *report.Characterisation, result report.Report) {
	known := map[string]bool{}
	for _, scaffold := range block.Scaffolds {
		known[scaffold.Address] = true
	}

	for _, mutant := range result.Mutants {
		if mutant.State != report.StructurallyUnassertable || known[mutant.Site] {
			continue
		}

		known[mutant.Site] = true

		block.Scaffolds = append(block.Scaffolds, report.Scaffold{
			ID:      characterise.Identify("scf-", mutant.Site),
			Kind:    "expect_failures",
			Address: mutant.Site,
			Status:  report.Scaffolded,
			Artefact: characterise.ArtefactFile(
				result.TestDirectory, scaffoldScenario,
			),
		})
	}

	slices.SortFunc(block.Scaffolds, func(left, right report.Scaffold) int {
		return strings.Compare(left.Address, right.Address)
	})
}

// scaffoldScenario names the artefact the scaffolds live in.
const scaffoldScenario = "scaffolds"

// stageSuite materialises the closure plus the current generated suite.
func stageSuite(
	configuration discovery.Configuration,
	scaffold characterise.Scaffold,
	block *report.Characterisation,
	target string,
) (sandbox.Sandbox, error) {
	// Re-rendered from the pins as they stand, not replayed from the report:
	// the report's view of a generated file is redacted, and a suite staged
	// from it would plan a redaction marker.
	staged := map[string][]byte{}

	for _, file := range pinnedFiles(scaffold, block.Pins) {
		if file.entry.Executable {
			staged[stagedPath(configuration, file.entry.Path)] = file.bytes
		}
	}

	return sandbox.Materialise(sandbox.Spec{
		SourceRoot: configuration.ClosureRoot,
		ModuleRel:  configuration.RootRelative(),
		Target:     target,
		Mutations:  nil,
		Staged:     staged,
		Share:      nil,
		Hardlink:   false,
	})
}

// absorb takes the verified suggestions the round produced and turns the ones
// at or below the chosen rung into pins.
//
// Only verified suggestions: an unverified one is a candidate the tool has not
// proven kills anything, and pinning it would put an unproven assertion into a
// suite whose whole claim is that everything in it was observed.
func absorb(block *report.Characterisation, result report.Report) int {
	// Keyed by scenario as well as expression: two scenarios legitimately need
	// the same rendered condition, and a global set would silently drop the
	// second one.
	known := map[string]bool{}
	for _, pin := range block.Pins {
		known[pin.Scenario+"\x00"+pin.Expression] = true
	}

	rung := characterise.Rung(block.Rung)
	added := 0

	for _, suggestion := range result.Suggestions {
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

		block.Pins = append(block.Pins, report.Pin{
			ID:       characterise.PinID(scenario, address, suggestion.Expression),
			Scenario: scenario, Address: address,
			Expression: suggestion.Expression, Status: report.Pinned, Reason: "",
			Rung: string(level),
		})
	}

	slices.SortFunc(block.Pins, func(left, right report.Pin) int {
		if order := strings.Compare(left.Scenario, right.Scenario); order != 0 {
			return order
		}

		return strings.Compare(left.Address, right.Address)
	})

	return added
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
