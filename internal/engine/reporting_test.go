package engine_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestPseudoTestedResourcesAreHeadlined(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	addresses := findingAddresses(result)
	if len(addresses) != 1 || addresses[0] != "terraform_data.ignored" {
		t.Fatalf("pseudo-tested list = %v, want [terraform_data.ignored]", addresses)
	}

	finding := result.Findings[0]
	if finding.Kind != report.PseudoTested {
		t.Fatalf("finding kind = %s, want %s", finding.Kind, report.PseudoTested)
	}

	if finding.Range.File == "" || finding.Range.Start.Line == 0 {
		t.Fatalf("finding is not navigable: %+v", finding.Range)
	}

	if len(finding.Mutants) == 0 {
		t.Fatal("finding carries no supporting mutants")
	}

	for _, id := range finding.Mutants {
		if _, found := result.MutantByID(id); !found {
			t.Fatalf("finding references unknown mutant %s", id)
		}
	}
}

func TestErrorKillsAreNeverEvidenceOfTesting(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	addresses := findingAddresses(result)
	if !slices.Contains(addresses, "terraform_data.app") {
		t.Fatalf("a resource whose only kill was Terraform's own error must still be "+
			"pseudo-tested; findings were %v", addresses)
	}
}

func TestZeroAssertionSuiteReportsNothingAsTested(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "count-indexed")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Baseline.Assertions != 0 {
		t.Fatalf("fixture is supposed to assert nothing, found %d assertions", result.Baseline.Assertions)
	}

	if !slices.Contains(findingAddresses(result), "terraform_data.node") {
		t.Fatalf("a zero-assertion suite must report the resource as pseudo-tested, got %v",
			findingAddresses(result))
	}

	if result.Metrics.AssertionScore != 0 {
		t.Fatalf("assertion score = %v, want 0 for a suite with no assertions",
			result.Metrics.AssertionScore)
	}
}

func TestIdentifiersAreStableAcrossRunsAndUnrelatedEdits(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")
	config := baseConfig(t, module)
	config.Preview = true

	first, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	second, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if identifiers(first) != identifiers(second) {
		t.Fatalf("identifiers changed between runs:\n%s\n%s", identifiers(first), identifiers(second))
	}

	edited := copyFixture(t, "skeleton")
	appendUnrelatedComment(t, edited+"/main.tf")

	editedConfig := baseConfig(t, edited)
	editedConfig.Preview = true

	third, err := engine.Run(t.Context(), editedConfig)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if identifiers(first) != identifiers(third) {
		t.Fatalf("an unrelated edit changed identifiers:\n%s\n%s", identifiers(first), identifiers(third))
	}
}

func identifiers(result report.Report) string {
	parts := make([]string, 0, len(result.Mutants))
	for _, mutant := range result.Mutants {
		parts = append(parts, mutant.ID+"="+mutant.Site)
	}

	return strings.Join(parts, ",")
}

func appendUnrelatedComment(t *testing.T, path string) {
	t.Helper()

	content := readFile(t, path)
	writeFile(t, path, "# an unrelated comment\n\n"+content)
}

func TestJSONReportCarriesEverythingNeededToAct(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	buffer := bytes.Buffer{}
	if err := report.WriteJSON(&buffer, result); err != nil {
		t.Fatalf("writing JSON: %v", err)
	}

	decoded := report.Report{} //nolint:exhaustruct // populated by the decoder.
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding JSON report: %v", err)
	}

	if decoded.SchemaVersion != report.SchemaVersion {
		t.Fatalf("schema version = %q, want %q", decoded.SchemaVersion, report.SchemaVersion)
	}

	if len(decoded.Mutants) != len(result.Mutants) {
		t.Fatalf("JSON lost mutants: %d vs %d", len(decoded.Mutants), len(result.Mutants))
	}

	for index, mutant := range decoded.Mutants {
		original := result.Mutants[index]

		if mutant.ID != original.ID || mutant.State != original.State || mutant.Diff != original.Diff {
			t.Fatalf("JSON mutant %d diverges from the report value", index)
		}
	}

	if len(decoded.Findings) != len(result.Findings) {
		t.Fatal("JSON lost findings")
	}

	if decoded.Metrics.MutationScore != result.Metrics.MutationScore {
		t.Fatal("JSON metrics diverge from the report value")
	}
}

func TestTerminalAndJSONReportsAgree(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	terminal := bytes.Buffer{}
	if err := report.WriteTerminal(&terminal, result); err != nil {
		t.Fatalf("writing terminal report: %v", err)
	}

	rendered := terminal.String()

	for _, finding := range result.Findings {
		if !strings.Contains(rendered, finding.Address) {
			t.Fatalf("terminal report omits finding %s", finding.Address)
		}
	}

	for _, mutant := range result.Survivors() {
		if !strings.Contains(rendered, mutant.Site) {
			t.Fatalf("terminal report omits survivor %s", mutant.Site)
		}
	}

	if !strings.Contains(rendered, "Mutation score") ||
		!strings.Contains(rendered, "Assertion score") ||
		!strings.Contains(rendered, "Reachability") {
		t.Fatalf("terminal report does not carry the three metrics:\n%s", rendered)
	}
}

// TestReportSatisfiesThePublishedSchema cross-checks a real run against the
// published contract: every required property present, and every state and
// operator the engine can emit named in the schema's enumerations. The deep
// structural validation lives beside the schema in internal/report.
func TestReportSatisfiesThePublishedSchema(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	buffer := bytes.Buffer{}
	if err := report.WriteJSON(&buffer, result); err != nil {
		t.Fatalf("writing JSON: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal(buffer.Bytes(), &document); err != nil {
		t.Fatalf("decoding JSON report: %v", err)
	}

	schema := map[string]any{}
	if err := json.Unmarshal(readSchema(t), &schema); err != nil {
		t.Fatalf("decoding schema: %v", err)
	}

	for _, key := range schemaStrings(schema, "required") {
		if _, present := document[key]; !present {
			t.Fatalf("report omits required property %q", key)
		}
	}

	states := enumeration(t, schema, "state")
	operators := enumeration(t, schema, "operator")

	for _, mutant := range result.Mutants {
		if !slices.Contains(states, string(mutant.State)) {
			t.Fatalf("state %q is not in the published schema", mutant.State)
		}

		if !slices.Contains(operators, mutant.Operator) {
			t.Fatalf("operator %q is not in the published schema", mutant.Operator)
		}
	}
}

func readSchema(t *testing.T) []byte {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	path := filepath.Join(root, "docs", "schema", "report-1.0.0.json")

	content, err := os.ReadFile(path) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return content
}

// enumeration reads one property's enum out of the mutant definition.
func enumeration(t *testing.T, schema map[string]any, property string) []string {
	t.Helper()

	described := schemaMap(schemaMap(schemaMap(schemaMap(schema, "$defs"), "mutant"), "properties"), property)

	names := schemaStrings(described, "enum")
	if len(names) == 0 {
		t.Fatalf("schema property %q has no enumeration", property)
	}

	return names
}

// schemaMap and schemaStrings walk decoded JSON without unchecked casts.
func schemaMap(document map[string]any, key string) map[string]any {
	nested, ok := document[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return nested
}

func schemaStrings(document map[string]any, key string) []string {
	values, ok := document[key].([]any)
	if !ok {
		return nil
	}

	names := make([]string, 0, len(values))

	for _, value := range values {
		if name, ok := value.(string); ok {
			names = append(names, name)
		}
	}

	return names
}
