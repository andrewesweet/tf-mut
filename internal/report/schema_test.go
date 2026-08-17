package report_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// schemaPath is the published contract the JSON reporter promises to keep.
const schemaPath = "../../docs/schema/report-2.1.0.json"

func TestPublishedSchemaMatchesTheReportersVersion(t *testing.T) {
	t.Parallel()

	schema := loadSchema(t)

	version := stringAt(mapAt(mapAt(schema, "properties"), "schema_version"), "const")
	if version != report.SchemaVersion {
		t.Fatalf("published schema is version %q, reporter emits %q", version, report.SchemaVersion)
	}

	name := filepath.Base(schemaPath)
	if !strings.Contains(name, report.SchemaVersion) {
		t.Fatalf("schema file %s does not name version %s", name, report.SchemaVersion)
	}
}

func TestReportValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(sampleReport())
	if err != nil {
		t.Fatalf("encoding report: %v", err)
	}

	document := any(nil)
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding report: %v", err)
	}

	if problems := validate(loadSchema(t), loadSchema(t), document, "$"); len(problems) > 0 {
		t.Fatalf("report does not validate against %s:\n  %s", schemaPath, strings.Join(problems, "\n  "))
	}
}

func TestSchemaRejectsAnUnknownState(t *testing.T) {
	t.Parallel()

	sample := sampleReport()
	sample.Mutants[0].State = "Teleported"

	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("encoding report: %v", err)
	}

	document := any(nil)
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding report: %v", err)
	}

	if problems := validate(loadSchema(t), loadSchema(t), document, "$"); len(problems) == 0 {
		t.Fatal("the schema accepted a state it does not define")
	}
}

// mapAt, stringsAt and stringAt walk decoded JSON without unchecked casts.
func mapAt(document map[string]any, key string) map[string]any {
	nested, ok := document[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return nested
}

func stringAt(document map[string]any, key string) string {
	value, ok := document[key].(string)
	if !ok {
		return ""
	}

	return value
}

func stringsAt(document map[string]any, key string) []string {
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

// The literals the sample report reuses.
const (
	sampleFile     = "main.tf"
	sampleRun      = "defaults"
	sampleResource = "terraform_data.app"
	sampleTestFile = "tests/unit.tftest.hcl"
	sampleMutantID = "0123456789ab"
	sampleOutput   = "output.tier"
)

// sampleReport exercises every branch of the schema the engine can produce.
func sampleReport() report.Report {
	return report.Report{
		SchemaVersion:    report.SchemaVersion,
		Command:          report.CommandRun,
		Module:           "/modules/example",
		TerraformVersion: "1.15.8",
		TestDirectory:    "tests",
		Baseline: report.Baseline{
			Runs: 1, Assertions: 2, DurationMS: 167,
			Fingerprint:        "0f4c0e6a",
			VolatileComponents: []string{"root_module.resources[terraform_data.app].values.id"},
		},
		Mutants:        sampleMutants(),
		Findings:       sampleFindings(),
		Metrics:        report.ComputeMetrics(sampleMutants()),
		OperatorErrors: report.ComputeOperatorErrors(sampleMutants()),
		Suppressions: []report.Suppression{{
			Kind:      "config-operator",
			Operators: []string{"STR-CASE"},
			Reason:    "casing is normalised downstream",
			Accepted:  true,
			Range:     nil,
			Mutants:   []string{sampleMutantID},
			Rejection: "",
		}},
		Warnings: []string{"1 file(s) are not canonically formatted"},
		Errors: []report.ExecutionError{{
			MutantID: sampleMutantID,
			Site:     sampleOutput,
			Message:  "no run block executed, so no verdict is possible",
		}},
		Population: report.Population{Selected: 2, Omitted: 3, Cached: 0, Fresh: 2},
		Selection:  report.Selection{Mode: report.SelectionSince, Ref: "main", ForcedFull: ""},
		Sampling:   &report.Sampling{RatePercent: 25, Seed: 42, Authoritative: false},
		Gates: &report.Gates{
			MinScore: report.GateOutcome{
				Evaluated: true, Scope: "selected", Partial: true, Passed: true, Refused: "",
			},
			FailOnNew: report.GateOutcome{
				Evaluated: false, Scope: "", Partial: false, Passed: false, Refused: "",
			},
			Baseline: &report.BaselineGate{
				Path: ".tf-mut-baseline.json", Accepted: 4, Matched: 3,
				New: []string{sampleMutantID}, Stale: nil,
				Unobserved:        []string{"fedcba987654"},
				StalenessReported: false, Write: "refused",
			},
		},
	}
}

func sampleFindings() []report.Finding {
	return []report.Finding{{
		ID:      "abcdef012345",
		Kind:    report.PseudoTested,
		Address: sampleResource,
		Module:  ".",
		Range: report.Range{
			File:  sampleFile,
			Start: report.Position{Line: 5, Column: 1},
			End:   report.Position{Line: 7, Column: 2},
		},
		Message: "1 extreme mutant(s) executed and no assertion caught any of them.",
		Mutants: []string{"ba9876543210"},
	}}
}

//nolint:funlen // one literal covering every optional member of the schema.
func sampleMutants() []report.Mutant {
	mutants := []report.Mutant{
		{
			ID:       sampleMutantID,
			Operator: "EXT-OUTPUT-NULL",
			Tier:     "smoke",
			Module:   ".",
			Site:     sampleOutput,
			Resource: "",
			Range: report.Range{
				File:  sampleFile,
				Start: report.Position{Line: 1, Column: 1},
				End:   report.Position{Line: 3, Column: 2},
			},
			Diff:  "--- a/main.tf\n+++ b/main.tf\n@@ -2 +2 @@\n-  value = \"x\"\n+  value = null\n",
			State: report.Survived,
			Verdict: &report.Verdict{
				Diagnosis: report.WeakAssertion,
				Message:   "an assertion reads output.tier and still passed",
				Fix:       "tighten the assertion at tests/unit.tftest.hcl:defaults",
				Evidence: report.Evidence{
					Delta: []report.Change{{
						Run:      sampleTestFile + "::" + sampleRun,
						Path:     "outputs.tier.value",
						Address:  sampleOutput,
						Baseline: `"critical"`,
						Mutant:   "null",
					}},
					UnknownPaths:       []string{"terraform_data.app.id"},
					VolatileComponents: []string{"root_module.resources[terraform_data.app].values.id"},
					UnstableAttributes: nil,
					Assertion:          sampleTestFile + ":" + sampleRun,
					ClosureVerdict:     "read through the output and local closure",
					DefeatedBy:         "",
					MockResource:       "",
				},
			},
			Runs: []report.RunOutcome{
				{File: sampleTestFile, Run: sampleRun, Phase: 1, Status: "pass"},
				{File: sampleTestFile, Run: sampleRun, Phase: 2, Status: "pass"},
			},
			Diagnostics:  nil,
			ExecutedRuns: 1,
			Validated:    false,
			Suppression:  nil,
			Provenance: &report.Provenance{
				Selection:      report.SelectionSince,
				Reason:         "main.tf changed since main",
				Execution:      report.ExecutionFresh,
				CacheKey:       "",
				BaselineStatus: "new",
			},
		},
		{
			ID:       "ba9876543210",
			Operator: "EXT-BODY-BLANK",
			Tier:     "smoke",
			Module:   ".",
			Site:     sampleResource,
			Resource: sampleResource,
			Range: report.Range{
				File:  sampleFile,
				Start: report.Position{Line: 5, Column: 1},
				End:   report.Position{Line: 7, Column: 2},
			},
			Diff:    "--- a/main.tf\n+++ b/main.tf\n@@ -6 +6,0 @@\n-  input = \"x\"\n",
			State:   report.KilledByError,
			Verdict: nil,
			Runs:    []report.RunOutcome{{File: sampleTestFile, Run: sampleRun, Phase: 1, Status: "error"}},
			Diagnostics: []report.Diagnostic{{
				Severity: "error",
				Summary:  "Attempt to get attribute from null value",
				Detail:   "This value is null, so it does not have any attributes.",
				Range: &report.Range{
					File:  sampleFile,
					Start: report.Position{Line: 9, Column: 11},
					End:   report.Position{Line: 9, Column: 40},
				},
				TestFile: sampleTestFile,
				TestRun:  sampleRun,
			}},
			ExecutedRuns: 1,
			Validated:    true,
			Suppression: &report.Suppression{
				Kind:      "comment",
				Operators: []string{"EXT-BODY-BLANK"},
				Reason:    "",
				Accepted:  false,
				Range: &report.Range{
					File:  sampleFile,
					Start: report.Position{Line: 4, Column: 1},
					End:   report.Position{Line: 4, Column: 30},
				},
				Mutants:   nil,
				Rejection: "no reason given, so the directive does not suppress",
			},
		},
	}

	return mutants
}

func loadSchema(t *testing.T) map[string]any {
	t.Helper()

	content, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("reading %s: %v", schemaPath, err)
	}

	schema := map[string]any{}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decoding %s: %v", schemaPath, err)
	}

	return schema
}

// validate is a deliberately small JSON Schema checker covering the keywords
// the published schema uses: type, required, properties, items, enum, const and
// local $ref. A dependency-free checker keeps the build chain's allow-list
// intact, and the schema is ours, so its vocabulary is ours to bound.
func validate(root, schema map[string]any, document any, path string) []string {
	if reference, ok := schema["$ref"].(string); ok {
		return validate(root, resolve(root, reference), document, path)
	}

	problems := []string{}

	if expected, ok := schema["type"].(string); ok && !matchesType(expected, document) {
		return []string{fmt.Sprintf("%s: want type %s, got %T", path, expected, document)}
	}

	if allowed, ok := schema["enum"].([]any); ok && !slices.Contains(allowed, document) {
		problems = append(problems, fmt.Sprintf("%s: %v is not one of %v", path, document, allowed))
	}

	if fixed, ok := schema["const"]; ok && document != fixed {
		problems = append(problems, fmt.Sprintf("%s: want %v, got %v", path, fixed, document))
	}

	switch typed := document.(type) {
	case map[string]any:
		problems = append(problems, validateObject(root, schema, typed, path)...)
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for index, element := range typed {
				problems = append(problems, validate(root, items, element, fmt.Sprintf("%s[%d]", path, index))...)
			}
		}
	default:
	}

	return problems
}

func validateObject(root, schema, document map[string]any, path string) []string {
	problems := []string{}

	for _, key := range stringsAt(schema, "required") {
		if _, present := document[key]; !present {
			problems = append(problems, fmt.Sprintf("%s: missing required property %q", path, key))
		}
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return problems
	}

	for key, value := range document {
		property, described := properties[key].(map[string]any)
		if !described {
			continue
		}

		problems = append(problems, validate(root, property, value, path+"."+key)...)
	}

	return problems
}

func resolve(root map[string]any, reference string) map[string]any {
	current := root

	for segment := range strings.SplitSeq(strings.TrimPrefix(reference, "#/"), "/") {
		next, ok := current[segment].(map[string]any)
		if !ok {
			return map[string]any{}
		}

		current = next
	}

	return current
}

func matchesType(expected string, document any) bool {
	switch expected {
	case "object":
		_, ok := document.(map[string]any)

		return ok
	case "array":
		_, ok := document.([]any)

		return ok
	case "string":
		_, ok := document.(string)

		return ok
	case "boolean":
		_, ok := document.(bool)

		return ok
	case "number":
		_, ok := document.(float64)

		return ok
	case "integer":
		number, ok := document.(float64)

		return ok && number == float64(int64(number))
	default:
		return true
	}
}
