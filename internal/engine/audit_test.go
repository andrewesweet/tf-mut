package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The closure rule, enforced rather than described: M2b and M2c cannot close the
// milestone while any M2a gate is red, so the gate has to name its
// reproductions and every name has to resolve to a test that exists.
//
// Without this, `just gate` would go green by naming nothing.

const (
	justfilePath = "Justfile"
	gateRecipe   = "gate:"

	// The two trees that hold this repository's Go sources.
	internalTree = "internal"
	commandTree  = "cmd"
)

// gateName matches one test name inside the recipe's -run pattern.
func gateName() *regexp.Regexp {
	return regexp.MustCompile(`Test[A-Za-z0-9]+`)
}

// declaration matches a Go test function definition.
func declaration(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
}

func TestTheHonestyGateNamesOnlyTestsThatExist(t *testing.T) {
	t.Parallel()

	named := gatedTests(t)
	if len(named) < minimumGateCases {
		t.Fatalf("the gate names %d reproductions, which is fewer than the milestone requires",
			len(named))
	}

	declared := testDeclarations(t)

	for _, name := range named {
		if !declared[name] {
			t.Fatalf("the gate names %s, which no test declares", name)
		}
	}
}

func TestTheHonestyGateCoversEveryNamedReproduction(t *testing.T) {
	t.Parallel()

	// The reproductions the milestone spec names, by the behaviour each proves.
	// A gate that stopped running one of these would still be green, which is
	// precisely the failure this catches.
	required := map[string]string{
		"R2-2 unknown-value refinement":             "TestUnknownRefinementSurvivesRatherThanBeingExcluded",
		"the conservative rule's other half":        "TestAKnownPayloadLetsTheOracleProveUnobservability",
		"R2-9 component-granular volatility":        "TestAVolatileTemplateStillYieldsAFindingOnItsStableSuffix",
		"a deterministic identifier stays killable": "TestADeterministicIdentifierIsNeverMasked",
		"C4 mutation-introduced volatility":         "TestVolatilityIntroducedOnlyByTheMutantIsDecidedByARerun",
		"verdict stability across runs":             "TestVolatilityFixturesClassifyIdenticallyAcrossRepeatedRuns",
		"R2-8 ordering and parallelism":             "TestStateAndDiagnosisAreIndependentOfOrderAndParallelism",
		"C3 output and local closure":               "TestADeltaSeenOnlyThroughALocalAndAnOutputIsNotNoAssertion",
		"the honest unasserted fallback":            "TestADefeatedClosureDiagnosesUnassertedAndNamesTheConstruct",
		"structurally unassertable constructs":      "TestConstructsWithNoProjectionAreStructurallyUnassertable",
		"diagnosis exclusivity":                     "TestTheHigherDiagnosisWinsWhenTwoPredicatesHold",
		"two-phase execution":                       "TestPhaseTwoRunsOnlyForPhaseOneSurvivors",
		"the pinned format-version range":           "TestAPayloadOutsideThePinnedVersionRangeIsAnOperationalFailure",
		"the streaming memory bound":                "TestVerboseDecodeOfALargeStreamStaysWithinTheHeapCeiling",
	}

	named := map[string]bool{}
	for _, name := range gatedTests(t) {
		named[name] = true
	}

	for behaviour, test := range required {
		if !named[test] {
			t.Fatalf("the gate does not run %s, which proves %s", test, behaviour)
		}
	}
}

// minimumGateCases guards against a recipe edit that empties the gate.
const minimumGateCases = 14

func gatedTests(t *testing.T) []string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	content, err := os.ReadFile(filepath.Join(root, justfilePath)) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", justfilePath, err)
	}

	recipe := string(content)

	start := strings.Index(recipe, gateRecipe)
	if start < 0 {
		t.Fatalf("%s declares no gate recipe", justfilePath)
	}

	recipe = recipe[start:]
	if end := strings.Index(recipe, "\n\n"); end > 0 {
		recipe = recipe[:end]
	}

	return gateName().FindAllString(recipe, -1)
}

// testDeclarations lists every test function the suite declares.
func testDeclarations(t *testing.T) map[string]bool {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	declared := map[string]bool{}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // a repository-owned path.
		if readErr != nil {
			return readErr //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		for _, name := range gateName().FindAllString(string(content), -1) {
			if declaration(name).MatchString(string(content)) {
				declared[name] = true
			}
		}

		return nil
	}

	if err := filepath.WalkDir(filepath.Join(root, "internal"), walk); err != nil {
		t.Fatalf("walking the suite: %v", err)
	}

	return declared
}

// TestEveryTestNameADocumentClaimsExists closes the hole the M4.5 review found
// in the gate-honesty pair.
//
// That pair checks that every name in a `just gate-*` recipe resolves to a
// declared test, and that every required behaviour is named by one. Neither
// direction sees a *document* naming a test nobody wrote — which is how
// `TestAMovedBlockInJSONRetainsTheFloor` came to be cited in a pull request
// and in an exit-gate document while existing nowhere in the tree. A claim
// about evidence is worth exactly as much as the evidence, and a reader who
// checks the claim finds nothing.
//
// The rule is deliberately weak: a `Test…` token in a document has to appear
// somewhere in the Go sources. That admits type and field names — `TestSuite`,
// `TestDirectory` — which documents legitimately mention, and rejects the one
// thing worth rejecting, a name that exists only in prose.
func TestEveryTestNameADocumentClaimsExists(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	sources := goSources(t, root)

	for _, document := range markdownDocuments(t, root) {
		content, err := os.ReadFile(document) //nolint:gosec // a repository-owned path.
		if err != nil {
			t.Fatalf("reading %s: %v", document, err)
		}

		for _, name := range gateName().FindAllString(string(content), -1) {
			if goConventionNames[name] {
				continue
			}

			if !strings.Contains(sources, name) {
				relative, _ := filepath.Rel(root, document)
				t.Errorf("%s names %s, which appears in no Go source", relative, name)
			}
		}
	}
}

// goConventionNames are Go's own, which a document may discuss without this
// repository declaring them.
//
//nolint:gochecknoglobals // an immutable allow-list.
var goConventionNames = map[string]bool{"TestMain": true}

// goSources is every Go source in the tree, concatenated.
func goSources(t *testing.T, root string) string {
	t.Helper()

	builder := strings.Builder{}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // a repository-owned path.
		if readErr != nil {
			return readErr //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		builder.Write(content)

		return nil
	}

	for _, tree := range []string{internalTree, commandTree} {
		if err := filepath.WalkDir(filepath.Join(root, tree), walk); err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}

	return builder.String()
}

// markdownDocuments lists the repository's own prose: the design record, the
// research record and the agent contract.
func markdownDocuments(t *testing.T, root string) []string {
	t.Helper()

	documents := []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "README.md")}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err //nolint:wrapcheck // the walker's own error, returned unchanged.
		}

		documents = append(documents, path)

		return nil
	}

	if err := filepath.WalkDir(filepath.Join(root, "docs"), walk); err != nil {
		t.Fatalf("walking docs: %v", err)
	}

	return documents
}
