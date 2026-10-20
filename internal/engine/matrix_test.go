package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// The applicability matrix is a living artefact, not a paragraph. These tests
// are what makes it one: a row describing an operator the catalogue does not
// enable, an enabled operator with no row, or a row whose operator nothing can
// fire on are all failures here.

const matrixPath = "docs/design/mutation-operators.md"

// operatorsFixture is the main matrix fixture, the one most tests preview.
const operatorsFixture = "operators"

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
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

	// The matrix's fixtures. Three operators need a shape the older fixtures
	// cannot carry: an alias needs a real provider to configure, a dynamic
	// block needs a provider whose schema declares a nested block type, which
	// neither offline provider has, and the Tier 4 lifecycle operators need
	// the `ignore_changes` and `replace_triggered_by` shapes the lifecycle
	// fixture stages under their kill witnesses.
	//
	// `mocked-aliases` needs the provider mirror, so without it the alias
	// operator has no site anywhere and the case skips rather than failing —
	// the repository's convention for a mirror-backed fixture.
	requireProviderMirror(t)

	fixtures := []string{operatorsFixture, "dynamic", "mocked-aliases", "lifecycle"}

	modules := make([]string, 0, len(fixtures))
	fired := map[string]bool{}

	for _, fixture := range fixtures {
		module := copyFixture(t, fixture)
		modules = append(modules, module)

		for _, mutant := range preview(t, module, nil).Mutants {
			fired[mutant.Operator] = true
		}
	}

	// The generated catalogue's site lives in the families fixture and fires
	// only behind its opt-in (M3e): the default populations above must never
	// carry it, so its site is witnessed by an opted-in preview.
	optedIn := previewRequest(t, copyFixture(t, "families"))
	optedIn.GeneratedFunctions = true

	familyResult, err := engine.Run(t.Context(), optedIn)
	if err != nil {
		t.Fatalf("families preview: %v", err)
	}

	for _, mutant := range familyResult.Mutants {
		fired[mutant.Operator] = true
	}

	// The pack form operators' sites live in the packs fixture and fire only
	// where a selected pack's entry parameterises them (M5c.1); a language
	// operator owns every row a pack entry also produced, so each form
	// operator is witnessed in isolation, as isolatedSite does.
	for _, operator := range []mutation.Operator{mutation.PackFlip, mutation.PackReplace, mutation.PackWidenCIDR} {
		packed := previewRequest(t, copyFixture(t, "packs"))
		packed.Packs = []string{"acme"}
		packed.IncludeOperators = []string{string(operator)}

		packResult, err := engine.Run(t.Context(), packed)
		if err != nil {
			t.Fatalf("packs preview: %v", err)
		}

		for _, mutant := range packResult.Mutants {
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

	for name, only := range map[string][]string{
		operatorsFixture: nil,
		"lifecycle":      admittedLifecycleOperators,
	} {
		result := preview(t, copyFixture(t, name), only)

		for _, warning := range result.Warnings {
			if strings.Contains(warning, "unparseable") {
				t.Fatalf("an operator emitted a mutant of %s that does not parse: %s", name, warning)
			}
		}

		if len(result.Mutants) == 0 {
			t.Fatalf("the %s fixture generated nothing", name)
		}
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

	//nolint:goconst // sample function names, shared with the family tables by nature.
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

	module := copyFixture(t, "mocked-aliases")

	swapped := preview(t, module, []string{"PROVIDER-ALIAS-SWAP"})
	if len(swapped.Mutants) == 0 {
		t.Fatal("no swap was generated between two mocked configurations")
	}

	// Remove the mock covering one alias. The swap must now be refused: it
	// would route execution to an unmocked provider past a gate that has run.
	tests := filepath.Join(module, "tests", "unit.tftest.hcl")
	content := readFile(t, tests)
	testsupport.WriteFile(t, tests, strings.Replace(content,
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

	warning := ""

	for _, candidate := range silent.Warnings {
		if strings.Contains(candidate, "says nothing about them") {
			warning = candidate

			break
		}
	}

	if warning == "" {
		t.Fatalf("a resource no operator reached produced no warning: %v", silent.Warnings)
	}

	// The fixture's unanswerable resource sits in the root module, whose
	// addresses carry no module path: the colon proves nothing precedes it.
	if !strings.Contains(warning, ": terraform_data.bare") {
		t.Fatalf("the warning does not name the bare root-module address unqualified: %s", warning)
	}

	answered := runFixture(t, "skeleton")

	if slices.ContainsFunc(answered.Warnings, func(warning string) bool {
		return strings.Contains(warning, "says nothing about them")
	}) {
		t.Fatalf("a fully covered module raised the unanswerable warning: %v", answered.Warnings)
	}
}

// The unanswered-helpers fixture: three same-named terraform_data.bare
// resources, none of which any enabled operator can reach. Two sit in
// production child modules and stay in the warning, qualified by their module
// path so the entries are distinguishable; the third sits in a helper module
// under the test directory, which the mutation surface has excluded since
// #191, so warning about it would grade the suite's scaffolding, not the
// module under test.
func TestTheUnanswerableWarningSkipsTestHelpersAndQualifiesModulePaths(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "unanswered-helpers")

	warning := ""

	for _, candidate := range result.Warnings {
		if strings.Contains(candidate, "says nothing about them") {
			warning = candidate

			break
		}
	}

	if warning == "" {
		t.Fatalf("a module with unanswerable resources raised no warning: %v", result.Warnings)
	}

	for _, path := range []string{"alpha/terraform_data.bare", "beta/terraform_data.bare"} {
		if !strings.Contains(warning, path) {
			t.Fatalf("the warning does not name %s by its module path: %s", path, warning)
		}
	}

	if strings.Contains(warning, "tests/") {
		t.Fatalf("the warning names a resource under the test directory: %s", warning)
	}

	if got := strings.Count(warning, "terraform_data.bare"); got != 2 {
		t.Fatalf("the warning names %d unanswerable resources, want the two production ones: %s", got, warning)
	}
}

func TestTheUnanswerableWarningIncludesProductionModulesUnderTheTestDirectory(t *testing.T) {
	t.Parallel()

	for _, testDirectory := range []string{engine.DefaultTestDirectory, "."} {
		for _, syntax := range []string{hclTestSyntax, jsonTestSyntax} {
			t.Run(testDirectory+"/"+syntax, func(t *testing.T) {
				t.Parallel()

				module := unansweredProductionModulesUnderTests(t, testDirectory, syntax)

				request := baseConfig(t, module)
				request.TestDirectory = testDirectory

				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run with production modules under tests: %v", err)
				}

				want := "3 resource(s) have no mutant under any enabled operator, so this run says nothing about them: " +
					"terraform_data.bare, tests/alpha/terraform_data.bare, tests/beta/terraform_data.bare"

				if !slices.Contains(result.Warnings, want) {
					t.Fatalf("warnings = %v, want %q", result.Warnings, want)
				}
			})
		}
	}
}

func unansweredProductionModulesUnderTests(t *testing.T, testDirectory, syntax string) string {
	t.Helper()

	module := copyFixture(t, "unanswered-helpers")
	for _, child := range []string{"alpha", "beta"} {
		if err := os.Rename(filepath.Join(module, child), filepath.Join(module, "tests", child)); err != nil {
			t.Fatalf("moving production child under tests: %v", err)
		}
	}

	testsupport.WriteFile(t, filepath.Join(module, mainFile), "resource \"terraform_data\" \"bare\" {}\n")

	if syntax == jsonTestSyntax {
		testsupport.WriteFile(t, filepath.Join(module, "modules.tf.json"),
			`{"module":{"alpha":{"source":"./tests/alpha"}}}`)
		testsupport.WriteFile(t, filepath.Join(module, "tests", "alpha", "modules.tf.json"),
			`{"module":{"beta":{"source":"../beta"}}}`)
	} else {
		appendFile(t, filepath.Join(module, mainFile), "\nmodule \"alpha\" { source = \"./tests/alpha\" }\n")
		appendFile(t, filepath.Join(module, "tests", "alpha", mainFile),
			"\nmodule \"beta\" { source = \"../beta\" }\n")
	}

	suitePath := filepath.Join(module, "tests", "unit.tftest.hcl")
	testsupport.WriteFile(t, suitePath, strings.ReplaceAll(readFile(t, suitePath),
		"module.alpha.ready && module.beta.ready", "module.alpha.ready && terraform_data.bare.input == null"))

	if testDirectory == "." {
		if err := os.Rename(suitePath, filepath.Join(module, "unit.tftest.hcl")); err != nil {
			t.Fatalf("moving suite to module root: %v", err)
		}
	}

	return module
}

func TestASampledRunDoesNotNameResourcesTheSampleMerelyDropped(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "sampled-unanswered")

	// The full population decides which resources the operators reached. The
	// sampled run's warning must be judged against that population: a resource
	// with mutants the sample did not keep is covered, not unanswerable.
	full, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	covered := map[string]bool{}

	for _, mutant := range full.Mutants {
		if mutant.Resource != "" {
			covered[mutant.Resource] = true
		}
	}

	if len(covered) < 2 {
		t.Fatalf("fixture drift: the covered resources are %v, want at least two", covered)
	}

	// Five percent of a population under twenty keeps exactly one mutant, so
	// at least one covered resource runs nothing while the population still
	// has its mutants.
	config := baseConfig(t, module)
	config.SamplePercent = 5
	config.HasSample = true

	sampled, err := engine.Run(t.Context(), &config)
	if err != nil {
		t.Fatalf("sampled run: %v", err)
	}

	if sampled.Sampling == nil {
		t.Fatal("the run did not record sampling")
	}

	if len(sampled.Mutants) == 0 {
		t.Fatalf("the sample kept nothing: %v", sampled.Metrics.Counts)
	}

	run := map[string]bool{}

	for _, mutant := range sampled.Mutants {
		if mutant.Resource != "" {
			run[mutant.Resource] = true
		}
	}

	dropped := []string{}

	for resource := range covered {
		if !run[resource] {
			dropped = append(dropped, resource)
		}
	}

	if len(dropped) == 0 {
		t.Fatal("the test is vacuous: every covered resource kept a sampled mutant")
	}

	warning := ""

	for _, candidate := range sampled.Warnings {
		if strings.Contains(candidate, "says nothing about them") {
			warning = candidate

			break
		}
	}

	if warning == "" {
		t.Fatalf("the sampled run raised no unanswerable warning at all: %v", sampled.Warnings)
	}

	for _, resource := range dropped {
		if strings.Contains(warning, resource) {
			t.Fatalf("the warning names %s, whose mutants the sample merely dropped: %s",
				resource, warning)
		}
	}

	if !strings.Contains(warning, "terraform_data.bare") {
		t.Fatalf("the warning does not name the resource no operator reached: %s", warning)
	}
}

// preview generates the population of a module without executing anything.
func preview(t *testing.T, module string, only []string) report.Report {
	t.Helper()

	config := previewRequest(t, module)
	config.IncludeOperators = only

	result, err := engine.Run(t.Context(), &config)
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
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

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

func TestIdentifiersSurviveALineMove(t *testing.T) {
	t.Parallel()

	// The M1 suite already proves stability across runs and unrelated edits.
	// This is the other half of the M4 disposition: the identifier carries no
	// line number, so moving the whole file down survives it.
	module := copyFixture(t, "skeleton")
	before := identifierSet(preview(t, module, nil))

	main := filepath.Join(module, "main.tf")
	testsupport.WriteFile(t, main, "\n\n"+readFile(t, main))

	if moved := identifierSet(preview(t, module, nil)); !slices.Equal(before, moved) {
		t.Fatalf("identifiers moved with the lines:\n  %v\n  %v", before, moved)
	}
}

func identifierSet(result report.Report) []string {
	found := make([]string, 0, len(result.Mutants))
	for _, mutant := range result.Mutants {
		found = append(found, mutant.ID)
	}

	slices.Sort(found)

	return found
}

// hunkHeader matches a unified diff's line range.
func hunkHeader() *regexp.Regexp {
	return regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+`)
}

func TestByteRangeOperatorsChangeOnlyTheLinesTheyOwn(t *testing.T) {
	t.Parallel()

	// The Tier 1 to Tier 3 operators rewrite byte ranges rather than re-printing
	// a block, so a template mutation must leave every byte outside its own
	// range alone. A whole-file diff would mean the rewrite went through a
	// printer that reformatted something it does not own.
	result := preview(t, copyFixture(t, "operators"), nil)

	for _, mutant := range result.Mutants {
		if mutation.TierOf(mutation.Operator(mutant.Operator)) == mutation.TierSmoke {
			continue
		}

		start, length, found := removedLines(mutant.Diff)
		if !found {
			continue
		}

		// The removed hunk must lie inside the lines the mutant's own range
		// spans, allowing for a deletion that takes the whole line with it.
		if start < mutant.Range.Start.Line || start+length-1 > mutant.Range.End.Line+1 {
			t.Fatalf("%s at %s:%d-%d rewrote lines %d-%d:\n%s",
				mutant.Operator, mutant.Range.File,
				mutant.Range.Start.Line, mutant.Range.End.Line,
				start, start+length-1, mutant.Diff)
		}
	}
}

// removedLines reads the original-side line range out of a unified diff hunk.
func removedLines(diff string) (start, length int, found bool) {
	match := hunkHeader().FindStringSubmatch(diff)
	if match == nil {
		return 0, 0, false
	}

	start, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, false
	}

	length = 1
	if match[2] != "" {
		if length, err = strconv.Atoi(match[2]); err != nil {
			return 0, 0, false
		}
	}

	return start, length, true
}
