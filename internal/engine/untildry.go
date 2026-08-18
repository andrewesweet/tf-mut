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
	workRoot string,
) error {
	convergence := &report.Convergence{
		Rounds: 0, NewPinsPerRound: []int{}, StopReason: "bounded",
	}
	block.Convergence = convergence

	for round := range defaultRounds {
		added, err := oneRound(ctx, runner, configuration, settings, version, block,
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
	target string,
) (int, error) {
	staged, err := stageSuite(configuration, block, target)
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
			ID:      "scf-" + characterise.Digest([]byte(mutant.Site))[:pinIDLength],
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
	block *report.Characterisation,
	target string,
) (sandbox.Sandbox, error) {
	staged := map[string][]byte{}

	for _, file := range block.Files {
		if file.Executable {
			staged[stagedPath(configuration, file.Path)] = []byte(file.Content)
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
	known := map[string]bool{}
	for _, pin := range block.Pins {
		known[pin.Expression] = true
	}

	rung := characterise.Rung(block.Rung)
	added := 0

	for _, suggestion := range result.Suggestions {
		if suggestion.Status != report.SuggestionVerified || known[suggestion.Expression] {
			continue
		}

		if !rung.Includes(rungOfExpression(suggestion.Expression)) {
			continue
		}

		scenario, found := scenarioForRun(block, suggestion.TargetRun)
		if !found {
			continue
		}

		known[suggestion.Expression] = true
		added++

		block.Pins = append(block.Pins, report.Pin{
			ID:       "pin-" + characterise.Digest([]byte(scenario + suggestion.Expression))[:pinIDLength],
			Scenario: scenario, Address: suggestion.Expression,
			Expression: suggestion.Expression, Status: report.Pinned, Reason: "",
			Rung: string(rungOfExpression(suggestion.Expression)),
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

const pinIDLength = 12

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
// The classification reads the expression because that is what a suggestion
// carries: the address adapter produces `output.<name>` for the contract
// surface and `length(...)` for a count, and everything else addresses a
// resource attribute, which is the configured rung.
func rungOfExpression(expression string) characterise.Rung {
	switch {
	case strings.HasPrefix(expression, "output."):
		return characterise.RungOutputs
	case strings.HasPrefix(expression, "length("):
		return characterise.RungCounts
	default:
		return characterise.RungConfigured
	}
}
