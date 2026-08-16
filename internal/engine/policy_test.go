package engine_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The operator identifiers the policy fixture's directives name.
const (
	strEmpty   = "STR-EMPTY"
	nullInject = "NULL-INJECT"
)

func TestConfiguredPolicyShapesThePopulation(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")

	for _, mutant := range result.Mutants {
		if mutant.Operator == "STR-CASE" {
			t.Fatalf("the configured operator exclusion did not apply: %s", mutant.ID)
		}

		if strings.HasPrefix(mutant.Range.File, "generated.tf") && mutant.State != report.Ignored {
			t.Fatalf("a mutant in an excluded path is %s, want %s", mutant.State, report.Ignored)
		}

		if mutant.Resource == "terraform_data.debug" && mutant.State != report.Ignored {
			t.Fatalf("a mutant in an excluded resource is %s, want %s", mutant.State, report.Ignored)
		}
	}

	if result.Count(report.Ignored) == 0 {
		t.Fatal("no exclusion fired at all")
	}
}

func TestAConfiguredMinimumScoreGatesTheRun(t *testing.T) {
	t.Parallel()

	// The file sets min_score, and nothing on the command line overrode it.
	result := runFixture(t, "policy")

	if !result.Metrics.Incomplete && result.Metrics.MutationScore == 0 {
		t.Fatal("the fixture scores nothing, so the gate would say nothing")
	}
}

func TestACommandLineFlagOverridesOneConfiguredScalarAndNoOthers(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "policy")

	overridden := baseConfig(t, module)
	overridden.Tier = "smoke"
	overridden.SetFlags = []string{engine.FlagTier}

	result, err := engine.Run(t.Context(), overridden)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.Tier != "smoke" {
			t.Fatalf("the tier flag did not override the configured tier: %s is %s",
				mutant.Operator, mutant.Tier)
		}
	}

	// The rest of the file still applies: the flag overrode one scalar, not the
	// whole policy.
	if result.Count(report.Ignored) == 0 {
		t.Fatal("overriding the tier discarded the configured exclusions")
	}
}

func TestADuplicateBlockIsAnError(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "policy")
	writeFile(t, filepath.Join(module, config.FileName),
		"operators {\n  tier = \"smoke\"\n}\n\noperators {\n  tier = \"standard\"\n}\n")

	if _, err := engine.Run(t.Context(), baseConfig(t, module)); !errors.Is(err, config.ErrConfig) {
		t.Fatalf("error = %v, want a configuration refusal", err)
	}
}

func TestAnIncludeAndExcludeConflictIsAnError(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "policy")
	writeFile(t, filepath.Join(module, config.FileName),
		"operators {\n  include = [\""+strEmpty+"\"]\n  exclude = [\""+strEmpty+"\"]\n}\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, config.ErrConfig) {
		t.Fatalf("error = %v, want a configuration refusal", err)
	}

	if !strings.Contains(err.Error(), strEmpty) {
		t.Fatalf("the refusal does not name the operator: %v", err)
	}
}

func TestAnUnknownSettingIsAnError(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "policy")
	writeFile(t, filepath.Join(module, config.FileName), "tf-mut {\n  min_scrore = 70\n}\n")

	if _, err := engine.Run(t.Context(), baseConfig(t, module)); !errors.Is(err, config.ErrConfig) {
		t.Fatalf("error = %v, want a configuration refusal", err)
	}
}

func TestConfigurationIsReadAtTheModuleRootAndNowhereElse(t *testing.T) {
	t.Parallel()

	root := copyFixture(t, "upward")
	// A file one level above the module under test must not change its policy.
	writeFile(t, filepath.Join(root, config.FileName),
		"operators {\n  exclude = [\"EXT-OUTPUT-NULL\"]\n}\n")

	result, err := engine.Run(t.Context(), baseConfig(t, filepath.Join(root, "root")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(mutantsWithOperator(result, "EXT-OUTPUT-NULL")) == 0 {
		t.Fatal("a configuration file outside the module root changed the population")
	}
}

func TestAReasonedSuppressionIgnoresTheMutantAndRecordsWhy(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")

	suppressed := false

	for _, mutant := range result.Mutants {
		if mutant.Site != "terraform_data.reasoned.input" || mutant.Operator != strEmpty {
			continue
		}

		suppressed = true

		if mutant.State != report.Ignored {
			t.Fatalf("a reasoned suppression left the mutant %s", mutant.State)
		}

		if mutant.Suppression == nil || mutant.Suppression.Reason == "" {
			t.Fatalf("the suppression records no reason: %+v", mutant.Suppression)
		}
	}

	if !suppressed {
		t.Fatalf("the suppressed site produced no mutant; sites are %v", sites(result))
	}
}

func TestAReasonlessSuppressionDoesNotSuppress(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")

	for _, mutant := range result.Mutants {
		if mutant.Site == "terraform_data.reasonless.input" &&
			mutant.Operator == strEmpty && mutant.State == report.Ignored {
			t.Fatal("a directive with no reason suppressed a finding")
		}
	}

	rejected := false

	for _, suppression := range result.Suppressions {
		if suppression.Accepted {
			continue
		}

		rejected = true

		if suppression.Rejection == "" {
			t.Fatalf("a rejected directive carries no explanation: %+v", suppression)
		}

		if len(suppression.Mutants) == 0 {
			t.Fatal("the rejected directive does not name the finding it tried to hide")
		}
	}

	if !rejected {
		t.Fatalf("the reasonless directive was not reported: %+v", result.Suppressions)
	}

	if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
		return strings.Contains(warning, "does not suppress")
	}) {
		t.Fatalf("no warning names the rejected directive: %v", result.Warnings)
	}
}

func TestSuppressionAttachesByOperatorIdentifier(t *testing.T) {
	t.Parallel()

	// Two operators fire on the same line; a directive naming one must leave the
	// other's finding standing.
	result := runFixture(t, "policy")

	states := map[string]report.State{}

	for _, mutant := range result.Mutants {
		if mutant.Site == "terraform_data.precise.input" {
			states[mutant.Operator] = mutant.State
		}
	}

	if states[nullInject] != report.Ignored {
		t.Fatalf("the named operator was not suppressed: %v", states)
	}

	if states[strEmpty] == report.Ignored {
		t.Fatalf("an unnamed operator on the same line was suppressed: %v", states)
	}
}

func TestConfigurationCannotHideAnUnmockedProviderFromTheSafetyGate(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "unmocked")
	// The exclusion covers every site in the module. The gate is decided from
	// the parsed configuration before generation, so it must still trip.
	writeFile(t, filepath.Join(module, config.FileName),
		"exclude {\n  paths = [\"main.tf\"]\n}\n")

	if _, err := engine.Run(t.Context(), baseConfig(t, module)); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want the real-infrastructure refusal", err)
	}
}

func TestConfigurationCannotHideAProvisionerFromTheSafetyGate(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	writeFile(t, filepath.Join(module, config.FileName),
		"exclude {\n  paths     = [\"main.tf\"]\n  resources = [\"terraform_data.effectful\"]\n}\n")

	if _, err := engine.Run(t.Context(), baseConfig(t, module)); !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want the unsandboxed-effects refusal", err)
	}
}

func TestIgnoredMutantsLeaveTheScoredSetAndTheGate(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")

	scored := result.Count(report.Killed) + result.Count(report.KilledByError) +
		result.Count(report.Survived) + result.Count(report.StructurallyUnassertable) +
		result.Count(report.NoCoverage) + result.Count(report.Timeout)

	if result.Metrics.Scored != scored {
		t.Fatalf("scored set = %d, want %d over the post-suppression population",
			result.Metrics.Scored, scored)
	}

	if result.Count(report.Ignored) == 0 {
		t.Fatal("nothing was ignored, so the assertion proves nothing")
	}
}

func TestSARIFCarriesTheNormativeResultSet(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "contract")

	document := sarifDocument(t, result)
	results := sarifResults(t, document)

	levels := map[string]string{}

	for _, entry := range results {
		levels[stringField(entry, "ruleId")] = stringField(entry, "level")
	}

	if levels["DEPENDS-DROP"] != "warning" {
		t.Fatalf("StructurallyUnassertable is at %q, want warning", levels["DEPENDS-DROP"])
	}

	for _, mutant := range result.Mutants {
		reported := slices.ContainsFunc(results, func(entry map[string]any) bool {
			return fingerprintOf(entry) == mutant.ID
		})

		wanted := mutant.State == report.Survived ||
			mutant.State == report.StructurallyUnassertable ||
			mutant.State == report.NoCoverage

		if reported != wanted {
			t.Fatalf("%s mutant %s: reported in SARIF = %v, want %v",
				mutant.State, mutant.ID, reported, wanted)
		}
	}
}

func TestSARIFLevelsSplitActionableFromIndeterminateSurvivors(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "unknown-refinement")

	for _, entry := range sarifResults(t, sarifDocument(t, result)) {
		mutant, found := result.MutantByID(fingerprintOf(entry))
		if !found || mutant.Verdict == nil {
			continue
		}

		want := "error"
		if !mutant.Verdict.Diagnosis.Actionable() {
			want = "note"
		}

		if got := stringField(entry, "level"); got != want {
			t.Fatalf("%s is at %q, want %q", mutant.Verdict.Diagnosis, got, want)
		}
	}
}

func TestSARIFOmitsSuppressedMutantsButKeepsRejectedDirectivesFindings(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")
	results := sarifResults(t, sarifDocument(t, result))

	for _, mutant := range result.Mutants {
		reported := slices.ContainsFunc(results, func(entry map[string]any) bool {
			return fingerprintOf(entry) == mutant.ID
		})

		if mutant.State == report.Ignored && reported {
			t.Fatalf("a suppressed mutant reached SARIF: %s", mutant.ID)
		}

		if mutant.Site == "terraform_data.reasonless.input" &&
			mutant.Operator == strEmpty && !reported {
			t.Fatal("a rejected directive removed a SARIF result")
		}
	}
}

func TestSARIFRulesCarryTheCatalogueDescription(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "policy")

	rules := sarifRules(t, sarifDocument(t, result))
	if len(rules) == 0 {
		t.Fatal("the SARIF document publishes no rules")
	}

	seen := map[string]bool{}

	for _, rule := range rules {
		id := stringField(rule, "id")
		seen[id] = true

		description := objectField(rule, "shortDescription")
		if stringField(description, "text") == "" || stringField(description, "text") == id {
			t.Fatalf("rule %s carries no catalogue description", id)
		}
	}

	for _, mutant := range result.Mutants {
		if !seen[mutant.Operator] {
			t.Fatalf("operator %s has no SARIF rule", mutant.Operator)
		}
	}
}

func TestSARIFValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "closure")

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	path := filepath.Join(root, "docs", "schema", "sarif-2.1.0.json")

	content, err := os.ReadFile(path) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading the SARIF schema: %v", err)
	}

	schema := map[string]any{}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decoding the SARIF schema: %v", err)
	}

	if problems := validateAgainst(schema, schema, sarifDocument(t, result), "$"); len(problems) > 0 {
		t.Fatalf("the SARIF document does not validate:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestEveryReporterDerivesFromTheSameReport(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "closure")

	terminal := bytes.Buffer{}
	if err := report.WriteTerminal(&terminal, result); err != nil {
		t.Fatalf("terminal: %v", err)
	}

	machine := bytes.Buffer{}
	if err := report.WriteJSON(&machine, result); err != nil {
		t.Fatalf("json: %v", err)
	}

	scanning := sarifDocument(t, result)

	for _, mutant := range result.Survivors() {
		if !strings.Contains(machine.String(), mutant.ID) {
			t.Fatalf("survivor %s is absent from the JSON report", mutant.ID)
		}

		if !strings.Contains(terminal.String(), string(mutant.Verdict.Diagnosis)) {
			t.Fatalf("diagnosis %s never reaches the terminal", mutant.Verdict.Diagnosis)
		}

		found := slices.ContainsFunc(sarifResults(t, scanning), func(entry map[string]any) bool {
			return fingerprintOf(entry) == mutant.ID
		})
		if !found {
			t.Fatalf("survivor %s is absent from SARIF", mutant.ID)
		}
	}
}

func sarifDocument(t *testing.T, result report.Report) map[string]any {
	t.Helper()

	buffer := bytes.Buffer{}
	if err := report.WriteSARIF(&buffer, result, engine.RuleDescriptions()); err != nil {
		t.Fatalf("writing SARIF: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal(buffer.Bytes(), &document); err != nil {
		t.Fatalf("decoding SARIF: %v", err)
	}

	return document
}

func sarifResults(t *testing.T, document map[string]any) []map[string]any {
	t.Helper()

	return objectsField(theRun(t, document), "results")
}

func sarifRules(t *testing.T, document map[string]any) []map[string]any {
	t.Helper()

	driver := objectField(objectField(theRun(t, document), "tool"), "driver")

	return objectsField(driver, "rules")
}

func theRun(t *testing.T, document map[string]any) map[string]any {
	t.Helper()

	runs := objectsField(document, "runs")
	if len(runs) != 1 {
		t.Fatalf("the SARIF document has %d runs, want one", len(runs))
	}

	return runs[0]
}

func stringField(document map[string]any, key string) string {
	value, ok := document[key].(string)
	if !ok {
		return ""
	}

	return value
}

func objectField(document map[string]any, key string) map[string]any {
	value, ok := document[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return value
}

func objectsField(document map[string]any, key string) []map[string]any {
	entries, ok := document[key].([]any)
	if !ok {
		return nil
	}

	objects := make([]map[string]any, 0, len(entries))

	for _, entry := range entries {
		if typed, ok := entry.(map[string]any); ok {
			objects = append(objects, typed)
		}
	}

	return objects
}

func fingerprintOf(entry map[string]any) string {
	return stringField(objectField(entry, "partialFingerprints"), "tfMutMutantId/v1")
}
