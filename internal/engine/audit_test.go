package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
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

	// The Go tool's machine-output switch, spelled once here so the package's
	// flag literals stay under goconst's repetition threshold.
	jsonFlag = "-json"
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

func TestExportedEngineTypesDeclareNoSeamControls(t *testing.T) {
	t.Parallel()

	compiled := compiledEnginePackage(t)
	auditExportedEngineTypes(t, compiled)

	// Dependencies is covered by the sweep above, and named here so the
	// composition root cannot stop being an exported engine type without
	// this test saying so.
	dependencies, found := compiled.Scope().Lookup("Dependencies").(*types.TypeName)
	if !found || dependencies == nil {
		t.Fatal("engine.Dependencies is not an exported type")
	}

	auditExportedEngineType(t, "Dependencies", dependencies.Type(), map[*types.Struct]bool{})
}

// TestThePackageCommentMapsEveryEngineFile holds doc.go's package comment
// honest about the package's own layout: every non-test Go file in the
// package directory has to be named in the map, so a file that moves, and a
// new one that arrives, forces the map to change with it. Without this the
// map would rot the way an unenforced document does — green while naming a
// layout that no longer exists.
func TestThePackageCommentMapsEveryEngineFile(t *testing.T) {
	t.Parallel()

	parsed, err := parser.ParseFile(token.NewFileSet(), "doc.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing doc.go: %v", err)
	}
	if parsed.Doc == nil {
		t.Fatal("doc.go carries no package comment to audit")
	}
	comment := parsed.Doc.Text()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	var unnamed []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		// Word boundaries, so naming characteriseprojection.go does not
		// silently satisfy projection.go: between two identifier characters
		// there is no boundary.
		named := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if !named.MatchString(comment) {
			unnamed = append(unnamed, name)
		}
	}

	if len(unnamed) > 0 {
		slices.Sort(unnamed)
		t.Fatalf("the package comment in doc.go fails to name %d of the package's non-test files: %s",
			len(unnamed), strings.Join(unnamed, ", "))
	}
}

func compiledEnginePackage(t *testing.T) *types.Package {
	t.Helper()

	packagePath := reflect.TypeFor[engine.Request]().PkgPath()
	// The arguments are fixed go-toolchain arguments built by the test:
	// packagePath is this package's own import path, not external input.
	arguments := []string{"list", "-mod=readonly", "-deps", "-export", jsonFlag, packagePath}
	command := exec.CommandContext(t.Context(), "go", arguments...) //nolint:gosec // fixed go-toolchain arguments.
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("compiling engine export data: %v\n%s", err, &stderr)
	}

	archives := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var entry struct {
			// The keys are the Go tool's own `go list -json` keys, PascalCase by
			// its convention; snake tags would not decode them.
			ImportPath string `json:"ImportPath"` //nolint:tagliatelle // go list's keys, not this tool's snake_case schema.
			Export     string `json:"Export"`     //nolint:tagliatelle // go list's keys, not this tool's snake_case schema.
		}
		decodeErr := decoder.Decode(&entry)
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			t.Fatalf("decoding compiler export paths: %v", decodeErr)
		}
		archives[entry.ImportPath] = entry.Export
	}

	lookup := func(path string) (io.ReadCloser, error) {
		return os.Open(archives[path])
	}
	compiled, err := importer.ForCompiler(token.NewFileSet(), "gc", lookup).Import(packagePath)
	if err != nil {
		t.Fatalf("importing compiled engine types: %v", err)
	}

	return compiled
}

func auditExportedEngineTypes(t *testing.T, compiled *types.Package) {
	t.Helper()

	for _, name := range compiled.Scope().Names() {
		typeName, ok := compiled.Scope().Lookup(name).(*types.TypeName)
		if ok && typeName.Exported() {
			auditExportedEngineType(t, name, typeName.Type(), map[*types.Struct]bool{})
		}
	}
}

func auditExportedEngineType(t *testing.T, name string, compiled types.Type, seen map[*types.Struct]bool) {
	t.Helper()

	underlying := compiled.Underlying()
	if pointer, ok := underlying.(*types.Pointer); ok {
		underlying = pointer.Elem().Underlying()
	}
	structure, ok := underlying.(*types.Struct)
	if !ok || seen[structure] {
		return
	}
	seen[structure] = true
	for field := range structure.Fields() {
		if field.Exported() && isSeamControl(field.Name()) {
			t.Errorf("exported engine type %s exposes seam field %s", name, field.Name())
		}
		if field.Embedded() {
			auditExportedEngineType(t, name, field.Type(), seen)
		}
	}
}

func isSeamControl(name string) bool {
	return strings.HasPrefix(name, "Seed") || strings.HasPrefix(name, "Disable") ||
		strings.HasSuffix(name, "ForTesting")
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
		"single-phase execution":                    "TestEveryMutantIsJudgedFromOneVerboseInvocation",
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
			return err // the walker's own error, returned unchanged.
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // a repository-owned path.
		if readErr != nil {
			return readErr // the walker's own error, returned unchanged.
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
			return err // the walker's own error, returned unchanged.
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // a repository-owned path.
		if readErr != nil {
			return readErr // the walker's own error, returned unchanged.
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
			return err // the walker's own error, returned unchanged.
		}

		documents = append(documents, path)

		return nil
	}

	if err := filepath.WalkDir(filepath.Join(root, "docs"), walk); err != nil {
		t.Fatalf("walking docs: %v", err)
	}

	return documents
}
