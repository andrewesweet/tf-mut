package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The identity twins: one attribute whose expression carries the same rewrite
// at several positions — the same numeric literal in both conditions, the same
// comparison operator at both, and a list of identical strings. The
// position-free identity hash cannot tell those rewrites apart, so before the
// occurrence ordinal every pair shared one identifier, and the verdict cache
// served one twin's verdict to the other.
const identicalEditsFixture = "identical-edits"

// The operators the fixture's twins collide under, named once for the tables
// and assertions below.
const (
	cmpEQNEOperator     = "CMP-EQ-NE"
	numNegateOperator   = "NUM-NEGATE"
	numOffByOneOperator = "NUM-OFF-BY-ONE"
	strCaseOperator     = "STR-CASE"
	strEmptyOperator    = "STR-EMPTY"
	boolNegateOperator  = "BOOL-NEGATE-INSERT"
)

// identicalEditsCounts is the collision power the fixture must carry: per
// operator, how many mutants the population has to hold. A count that falls
// short means an operator stopped firing at one of the twin positions and the
// uniqueness claim below has gone vacuous.
var identicalEditsCounts = map[string]int{ //nolint:gochecknoglobals // the fixture's immutable collision table.
	boolNegateOperator:  2, // one per `var.a` reference
	cmpEQNEOperator:     2, // one per `==` in the conditional
	numNegateOperator:   2, // one per `1`
	numOffByOneOperator: 4, // one per direction, at each of the two `1`s
	strCaseOperator:     5, // first/second/third, plus the two "same" elements
	strEmptyOperator:    5, // first/second/third, plus the two "same" elements
}

// TestIdenticalRewritesInOneAttributeGetDistinctIDs holds the published
// identity rule at its collision case: every mutant of one population carries
// an identifier no other mutant shares, however identical their rewrites.
func TestIdenticalRewritesInOneAttributeGetDistinctIDs(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, identicalEditsFixture)

	result, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	counts := map[string]int{}

	for _, mutant := range result.Mutants {
		if mutant.Range.File != mainFile {
			continue
		}

		counts[mutant.Operator]++
	}

	for operator, want := range identicalEditsCounts {
		if counts[operator] != want {
			t.Errorf("operator %s produced %d mutants, want %d; the fixture lost a twin",
				operator, counts[operator], want)
		}
	}

	seen := map[string]bool{}

	for _, mutant := range result.Mutants {
		if seen[mutant.ID] {
			t.Errorf("mutant identifier %s is carried by more than one mutant (%s at %s)",
				mutant.ID, mutant.Operator, mutant.Site)
		}

		seen[mutant.ID] = true
	}
}

// TestACachedReplayAgreesWithAFreshRunOnIdenticalRewrites is the D5
// reproduction: two identical rewrites in one attribute, one killed by the
// suite and one surviving it, run three times — cold, warm from the cache,
// and fresh with the cache off. Whichever twin wrote the cache entry last,
// the warm run must not serve one twin's verdict to the other: every warm
// verdict equals the fresh verdict at the same position.
func TestACachedReplayAgreesWithAFreshRunOnIdenticalRewrites(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, identicalEditsFixture)
	config := baseConfig(t, module)

	cold := cachedRun(t, config)
	if cold.Population.Cached != 0 {
		t.Fatalf("the cold run reported %d cached verdicts from an empty cache",
			cold.Population.Cached)
	}

	warm := cachedRun(t, config)
	if warm.Population.Cached == 0 || warm.Population.Fresh != 0 {
		t.Fatalf("the warm run was not all cache hits: %+v", warm.Population)
	}

	fresh := config
	fresh.NoCache = true
	uncached := cachedRun(t, fresh)

	// The fixture's verdicts must actually diverge between twins, or the
	// replay below has nothing to get wrong.
	pick := pickMutants(uncached)
	killed, spaced := 0, 0

	for _, mutant := range pick {
		switch mutant.State { //nolint:exhaustive // only the executed evidence matters here.
		case report.Killed:
			killed++
		case report.Survived, report.StructurallyUnassertable, report.Unobservable:
			spaced++
		default:
			// other states are not divergence evidence either way.
		}
	}

	if killed == 0 || spaced == 0 {
		t.Fatalf("the twins' verdicts do not diverge (%d killed, %d not): "+
			"a replay flip would be invisible; states are %s",
			killed, spaced, describeStates(pick))
	}

	// Compare position by position: identifier plus source range, because the
	// defect under test is exactly that an identifier once named two
	// positions.
	freshByPosition := map[string]report.Mutant{}
	for _, mutant := range uncached.Mutants {
		freshByPosition[twinPosition(mutant)] = mutant
	}

	for _, mutant := range warm.Mutants {
		reference, found := freshByPosition[twinPosition(mutant)]
		if !found {
			t.Fatalf("warm mutant %s at %s has no fresh counterpart",
				mutant.ID, twinPosition(mutant))
		}

		if mutant.State != reference.State {
			t.Errorf("mutant %s at %s: warm state %s, fresh state %s — "+
				"the cache served one twin's verdict to the other",
				mutant.ID, twinPosition(mutant), mutant.State, reference.State)
		}
	}
}

// TestMutantIDsSurviveAnEditEarlierInTheSameAttribute pins the choice of
// occurrence ordinal over byte offset as the collision disambiguator: every
// byte offset inside one attribute shifts under an edit earlier in it —
// renaming the variable the first condition reads is enough — while the
// ordinal of a twin changes only when its group's membership or order
// changes. The populations are matched by occurrence order within each
// operator — an order-preserving rename keeps it — and after the rename the
// twins whose own text is untouched keep their identifiers; the
// BOOL-NEGATE-INSERT twins legitimately move, because the rename rewrote
// the expression their rewrite replaces.
func TestMutantIDsSurviveAnEditEarlierInTheSameAttribute(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, identicalEditsFixture)

	before, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview before the rename: %v", err)
	}

	renameVariable(t, module, "a", "aa")

	after, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview after the rename: %v", err)
	}

	stable := map[string]bool{
		cmpEQNEOperator:     true, // the operators are not part of the rename
		numNegateOperator:   true,
		numOffByOneOperator: true,
		strCaseOperator:     true,
		strEmptyOperator:    true,
	}

	for operator := range stable {
		beforeLeg, afterLeg := orderedByPosition(before, operator), orderedByPosition(after, operator)
		if len(beforeLeg) != len(afterLeg) {
			t.Fatalf("operator %s holds %d mutants before the rename and %d after",
				operator, len(beforeLeg), len(afterLeg))
		}

		for index := range beforeLeg {
			if beforeLeg[index].ID != afterLeg[index].ID {
				t.Errorf("%s occurrence %d (at %d:%d) moved from %s to %s under a rename it does not touch",
					operator, index, afterLeg[index].Range.Start.Line, afterLeg[index].Range.Start.Column,
					beforeLeg[index].ID, afterLeg[index].ID)
			}
		}
	}
}

// TestMutantIDsAreIndependentOfTheOperatorSelection holds the placement of
// the disambiguation pass before any selection lever: the identifier is a
// property of the source and the rewrite, not of the run that asked for it,
// so a population restricted by operator selection carries the same
// identifiers as the full population. The cache, the baseline and the
// suppression directives all key on identifiers across differently-selected
// runs.
func TestMutantIDsAreIndependentOfTheOperatorSelection(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, identicalEditsFixture)

	full, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview of the full population: %v", err)
	}

	request := previewRequest(t, module)
	request.IncludeOperators = []string{numOffByOneOperator}

	narrow, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("preview of the restricted population: %v", err)
	}

	wide, narrowByOrder := orderedByPosition(full, numOffByOneOperator), orderedByPosition(narrow, numOffByOneOperator)
	if len(wide) != 4 || len(narrowByOrder) != 4 {
		t.Fatalf("the fixture carries %d full and %d restricted NUM-OFF-BY-ONE mutants, want 4 and 4",
			len(wide), len(narrowByOrder))
	}

	for index := range wide {
		if wide[index].ID != narrowByOrder[index].ID {
			t.Errorf("occurrence %d at %d:%d is %s in the full population and %s under operator selection — "+
				"identifiers must not depend on the run's selection",
				index, wide[index].Range.Start.Line, wide[index].Range.Start.Column,
				wide[index].ID, narrowByOrder[index].ID)
		}
	}
}

// orderedByPosition returns one operator's mutants of the module's main file
// in source order, the order the occurrence ordinals are assigned in. Equal
// start positions — the two directions of NUM-OFF-BY-ONE share one — order by
// their diff, which is stable for one source text.
func orderedByPosition(result report.Report, operator string) []report.Mutant {
	pick := []report.Mutant{}

	for _, mutant := range result.Mutants {
		if mutant.Range.File == mainFile && mutant.Operator == operator {
			pick = append(pick, mutant)
		}
	}

	slices.SortFunc(pick, func(left, right report.Mutant) int {
		if left.Range.Start.Line != right.Range.Start.Line {
			return left.Range.Start.Line - right.Range.Start.Line
		}

		if left.Range.Start.Column != right.Range.Start.Column {
			return left.Range.Start.Column - right.Range.Start.Column
		}

		return strings.Compare(left.Diff, right.Diff)
	})

	return pick
}

// renameVariable renames a root variable across the fixture's module: an
// edit earlier in the conditional that shifts every later byte offset in the
// attribute without touching the rewrites that follow it. The suite file is
// left alone — these tests only preview, and a preview never executes it.
func renameVariable(t *testing.T, module, from, to string) {
	t.Helper()

	path := filepath.Join(module, mainFile)
	source, err := os.ReadFile(path) //nolint:gosec // a test-owned fixture copy's path.
	if err != nil {
		t.Fatalf("reading %s: %v", mainFile, err)
	}

	rewritten := strings.ReplaceAll(string(source), "var."+from, "var."+to)
	rewritten = strings.ReplaceAll(rewritten, "variable \""+from+"\"", "variable \""+to+"\"")

	//nolint:gosec // a test-owned fixture copy's path.
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatalf("writing %s: %v", mainFile, err)
	}
}

// pickMutants narrows a report to the conditional's mutants — the site whose
// twins the suite separates.
func pickMutants(result report.Report) []report.Mutant {
	pick := []report.Mutant{}

	for _, mutant := range result.Mutants {
		if mutant.Range.File == mainFile && mutant.Site == "local.pick" {
			pick = append(pick, mutant)
		}
	}

	return pick
}

// twinPosition names a mutant by identifier and position, so twins that share
// an identifier stay distinct.
func twinPosition(mutant report.Mutant) string {
	return fmt.Sprintf("%s|%s|%d:%d", mutant.ID, mutant.Range.File,
		mutant.Range.Start.Line, mutant.Range.Start.Column)
}

func describeStates(mutants []report.Mutant) string {
	states := make([]string, 0, len(mutants))
	for _, mutant := range mutants {
		states = append(states, string(mutant.State))
	}

	slices.Sort(states)

	return strings.Join(states, ",")
}
