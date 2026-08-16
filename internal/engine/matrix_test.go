package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The applicability matrix is a living artefact, not a paragraph. These tests
// are what makes it one: a row describing an operator the catalogue does not
// enable, an enabled operator with no row, or a row whose operator nothing can
// fire on are all failures here.

const matrixPath = "docs/design/mutation-operators.md"

// matrixRow matches the operator column of the matrix table.
func matrixRow() *regexp.Regexp {
	return regexp.MustCompile("(?m)^\\| `([A-Z][A-Z0-9-]+)` \\|")
}

func TestEveryEnabledOperatorHasAMatrixRow(t *testing.T) {
	t.Parallel()

	rows := matrixOperators(t)

	for _, entry := range mutation.Catalogue() {
		if !slices.Contains(rows, string(entry.Operator)) {
			t.Fatalf("operator %s is enabled but has no row in %s", entry.Operator, matrixPath)
		}
	}
}

func TestEveryMatrixRowNamesAnEnabledOperator(t *testing.T) {
	t.Parallel()

	catalogue := mutation.Catalogue()

	enabled := make([]string, 0, len(catalogue))
	for _, entry := range catalogue {
		enabled = append(enabled, string(entry.Operator))
	}

	for _, row := range matrixOperators(t) {
		if !slices.Contains(enabled, row) {
			t.Fatalf("%s describes %s, which the catalogue does not enable", matrixPath, row)
		}
	}
}

func TestEveryEnabledOperatorHasAGenerationSite(t *testing.T) {
	t.Parallel()

	// The matrix's fixtures. Two operators need a shape the main fixture cannot
	// carry: an alias needs a real provider to configure, and a dynamic block
	// needs a provider whose schema declares a nested block type, which neither
	// offline provider has.
	fixtures := []string{"operators", "dynamic"}
	if _, mirrored := cliConfigFile(t); mirrored {
		fixtures = append(fixtures, "aliases")
	}

	modules := make([]string, 0, len(fixtures))
	fired := map[string]bool{}

	for _, fixture := range fixtures {
		module := copyFixture(t, fixture)
		modules = append(modules, module)

		for _, mutant := range preview(t, module, nil).Mutants {
			fired[mutant.Operator] = true
		}
	}

	for _, entry := range mutation.Catalogue() {
		if fired[string(entry.Operator)] || isolatedSite(t, modules, entry.Operator) {
			continue
		}

		t.Fatalf("operator %s has no generation site in the matrix fixtures %v",
			entry.Operator, fixtures)
	}
}

func TestTheMatrixFixtureGeneratesOnlyParseableMutants(t *testing.T) {
	t.Parallel()

	result := preview(t, copyFixture(t, "operators"), nil)

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "unparseable") {
			t.Fatalf("an operator emitted a mutant that does not parse: %s", warning)
		}
	}

	if len(result.Mutants) == 0 {
		t.Fatal("the matrix fixture generated nothing")
	}
}

func TestPreviewCoversTheNewOperatorsWithoutExecutingTheSuite(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "operators")
	before := treeDigest(t, module)

	result := preview(t, module, nil)

	for _, mutant := range result.Mutants {
		if mutant.State != report.Pending {
			t.Fatalf("preview mutant %s has state %s", mutant.ID, mutant.State)
		}

		if len(mutant.Runs) != 0 {
			t.Fatalf("preview mutant %s executed %d runs", mutant.ID, len(mutant.Runs))
		}
	}

	if result.Baseline.Runs != 0 {
		t.Fatalf("preview executed %d baseline runs", result.Baseline.Runs)
	}

	assertTreeUnchanged(t, module, before)
}

func TestIdenticalRewritesAreDeduplicatedAcrossOperators(t *testing.T) {
	t.Parallel()

	result := preview(t, copyFixture(t, "operators"), nil)

	seen := map[string]string{}

	for _, mutant := range result.Mutants {
		key := mutant.Range.File + "\x00" + mutant.Diff

		if previous, found := seen[key]; found {
			t.Fatalf("%s and %s produced the same rewrite of %s",
				previous, mutant.Operator, mutant.Range.File)
		}

		seen[key] = mutant.Operator
	}
}

func TestTypeInvalidMutantsAreNotGeneratedWhereTheEvidenceDecides(t *testing.T) {
	t.Parallel()

	result := preview(t, copyFixture(t, "operators"), nil)

	for _, mutant := range result.Mutants {
		// The variable declares nullable = false, so a null default is doomed.
		if mutant.Operator == "VAR-DEFAULT-NULL" && mutant.Site == "var.strict.default" {
			t.Fatal("a null default was generated for a variable that rejects null")
		}

		// The schema does not describe a `provider` argument, so the deletion
		// operators must leave the meta-arguments alone.
		if mutant.Operator == "EXT-ATTR-DELETE" && strings.HasSuffix(mutant.Site, ".count") {
			t.Fatal("a meta-argument was deleted by a schema-gated operator")
		}
	}
}

func TestTheCuratedFunctionListIsClosed(t *testing.T) {
	t.Parallel()

	// The boundary against M3's metadata-driven catalogue, asserted rather than
	// described: nothing here consults Terraform's function metadata, so an
	// operator may only fire on a name the curated table lists.
	curated := mutation.CuratedFunctions()

	result := preview(t, copyFixture(t, "operators"), nil)

	for _, mutant := range result.Mutants {
		if !strings.HasPrefix(mutant.Operator, "FN-") {
			continue
		}

		if !slices.ContainsFunc(curated, func(name string) bool {
			return strings.Contains(mutant.Diff, name+"(")
		}) {
			t.Fatalf("%s fired on a function outside the curated list:\n%s",
				mutant.Operator, mutant.Diff)
		}
	}

	for _, name := range []string{"substr", "contains", "length", "upper"} {
		if name == "upper" {
			continue
		}

		if slices.Contains(curated, name) {
			t.Fatalf("%s is in the curated list; the metadata-driven catalogue is M3", name)
		}
	}
}

func TestValidationWeakeningEmitsTheFormThatValidates(t *testing.T) {
	t.Parallel()

	// The R1 M12 failure mode: `condition = true` is rejected by Terraform,
	// because a validation condition must refer to its own variable, so the
	// operator would be 100% Invalid. This is the form that validates.
	result := preview(t, copyFixture(t, "operators"), []string{"VAR-VALIDATION-WEAKEN"})

	if len(result.Mutants) == 0 {
		t.Fatal("the weakening operator produced nothing")
	}

	for _, mutant := range result.Mutants {
		if !strings.Contains(mutant.Diff, "can(var.environment)") {
			t.Fatalf("the weakened condition is not the validating form:\n%s", mutant.Diff)
		}
	}
}

func TestForEachToCountRewritesEveryInstanceKeyReference(t *testing.T) {
	t.Parallel()

	result := preview(t, copyFixture(t, "operators"), []string{"FOREACH-TO-COUNT"})

	if len(result.Mutants) == 0 {
		t.Fatal("the coordinated rewrite produced nothing")
	}

	for _, mutant := range result.Mutants {
		if !strings.Contains(mutant.Diff, "count = length(") {
			t.Fatalf("the for_each was not rewritten as a count:\n%s", mutant.Diff)
		}

		if !strings.Contains(mutant.Diff, "count.index") {
			t.Fatalf("the instance-key references were not rewritten:\n%s", mutant.Diff)
		}

		for line := range strings.SplitSeq(mutant.Diff, "\n") {
			if strings.HasPrefix(line, "+") && strings.Contains(line, "each.") {
				t.Fatalf("the rewrite left an each reference behind:\n%s", mutant.Diff)
			}
		}
	}
}

func TestProviderAliasSwapKeepsMockStatus(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "aliases")

	swapped := preview(t, module, []string{"PROVIDER-ALIAS-SWAP"})
	if len(swapped.Mutants) == 0 {
		t.Fatal("no swap was generated between two mocked configurations")
	}

	// Remove the mock covering one alias. The swap must now be refused: it
	// would route execution to an unmocked provider past a gate that has run.
	tests := filepath.Join(module, "tests", "unit.tftest.hcl")
	content := readFile(t, tests)
	writeFile(t, tests, strings.Replace(content,
		"mock_provider \"null\" {\n  alias = \"secondary\"\n}\n\n", "", 1))

	guarded := preview(t, module, []string{"PROVIDER-ALIAS-SWAP"})
	if len(guarded.Mutants) != 0 {
		t.Fatalf("a swap from a mocked to an unmocked configuration was generated:\n%s",
			guarded.Mutants[0].Diff)
	}
}

func TestPerOperatorErrorCountsAreReported(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "closure")

	if len(result.OperatorErrors) == 0 {
		t.Fatal("the report carries no per-operator counts")
	}

	generated := 0

	for _, counts := range result.OperatorErrors {
		generated += counts.Generated

		if counts.Generated == 0 {
			t.Fatalf("operator %s is counted without having generated anything", counts.Operator)
		}

		if counts.Invalid > counts.Generated {
			t.Fatalf("operator %s reports more invalid mutants than it generated", counts.Operator)
		}
	}

	if generated != len(result.Mutants) {
		t.Fatalf("per-operator counts total %d, want %d", generated, len(result.Mutants))
	}
}

func TestAResourceNoOperatorCanMutateIsWarnedAbout(t *testing.T) {
	t.Parallel()

	// The warning is redefined over every enabled tier: it fires only when no
	// enabled operator produced any mutant for a resource.
	silent := runFixture(t, "no-optional-arguments")

	if !slices.ContainsFunc(silent.Warnings, func(warning string) bool {
		return strings.Contains(warning, "says nothing about them")
	}) {
		t.Fatalf("a resource no operator reached produced no warning: %v", silent.Warnings)
	}

	answered := runFixture(t, "skeleton")

	if slices.ContainsFunc(answered.Warnings, func(warning string) bool {
		return strings.Contains(warning, "says nothing about them")
	}) {
		t.Fatalf("a fully covered module raised the unanswerable warning: %v", answered.Warnings)
	}
}

// preview generates the population of a module without executing anything.
func preview(t *testing.T, module string, only []string) report.Report {
	t.Helper()

	config := baseConfig(t, module)
	config.Preview = true
	config.IncludeOperators = only

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	return result
}

func matrixOperators(t *testing.T) []string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	content, err := os.ReadFile(filepath.Join(root, matrixPath)) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", matrixPath, err)
	}

	// Only the matrix section is normative. The tier tables above it are prose
	// about the catalogue's shape, and include operators later milestones own.
	section := string(content)

	start := strings.Index(section, "## Applicability matrix")
	if start < 0 {
		t.Fatalf("%s has no applicability matrix", matrixPath)
	}

	section = section[start:]
	if end := strings.Index(section, "\n## "); end > 0 {
		section = section[:end]
	}

	matches := matrixRow().FindAllStringSubmatch(section, -1)

	operators := make([]string, 0, len(matches))
	for _, match := range matches {
		operators = append(operators, match[1])
	}

	if len(operators) == 0 {
		t.Fatalf("%s contains no matrix rows", matrixPath)
	}

	return operators
}

// isolatedSite reports whether an operator generates anything when nothing else
// competes with it.
//
// An operator absent from the full population may still have a site:
// deduplication is by mutated content, so one operator can subsume another
// wherever both rewrite a file identically. Isolating it settles which it is.
func isolatedSite(t *testing.T, modules []string, operator mutation.Operator) bool {
	t.Helper()

	for _, module := range modules {
		if len(preview(t, module, []string{string(operator)}).Mutants) > 0 {
			return true
		}
	}

	return false
}

func TestEveryOperatorInTheMatrixFixtureClassifiesThroughTheStateModel(t *testing.T) {
	t.Parallel()

	// The matrix's end-to-end column: every operator that fires on the fixture
	// reaches a state the normative table defines, and none is left Pending or
	// turned into an operational failure.
	result := runFixture(t, "operators")

	legal := []report.State{
		report.Invalid, report.Killed, report.KilledByError, report.Timeout,
		report.Survived, report.StructurallyUnassertable, report.Unobservable,
		report.NoCoverage, report.Ignored,
	}

	classified := map[string]bool{}

	for _, mutant := range result.Mutants {
		if !slices.Contains(legal, mutant.State) {
			t.Fatalf("%s mutant %s reached state %s, which the model does not define",
				mutant.Operator, mutant.ID, mutant.State)
		}

		if mutant.State == report.Survived && mutant.Verdict == nil {
			t.Fatalf("survivor %s carries no diagnosis", mutant.ID)
		}

		classified[mutant.Operator] = true
	}

	if len(result.Errors) > 0 {
		t.Fatalf("the matrix fixture produced %d operational failures: %+v",
			len(result.Errors), result.Errors[0])
	}

	if len(classified) < minimumOperatorsExercised {
		t.Fatalf("only %d operators were exercised end to end", len(classified))
	}
}

// minimumOperatorsExercised guards against a fixture edit that quietly stops
// exercising most of the catalogue.
const minimumOperatorsExercised = 50
