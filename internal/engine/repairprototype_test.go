package engine_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M5-0.5b is deliberately test-only. This file is the pinned throwaway repair
// table, its structured-diagnostic adapter and the offline acceptance pairs.
// The integration-tagged corpus run is in repairprototype_integration_test.go;
// no production package imports or calls anything declared here.

const (
	repairDiagnosticFixture = "repair-diagnostic"
	repairTypeString        = "string"
	repairVariableCIDR      = "cidr"
	repairVariableRegion    = "region"
	repairTraversalCIDR     = "var.cidr"

	// repairCandidateTableJSON is the table pinned before the prototype runs.
	// Runtime diagnostics are intentionally absent from its shape: initial
	// lookup accepts only a todos judgement point and the variable declaration.
	repairCandidateTableJSON = `{
  "version": 1,
  "rules": [
    {
      "name": "cidr-functions",
      "functions": ["cidrnetmask", "cidrsubnet", "cidrhost", "cidrsubnets"],
      "requires_arn_prefix": false,
      "candidates": ["\"10.0.0.0/16\"", "\"192.168.0.0/24\""]
    },
    {
      "name": "arn-functions",
      "functions": ["regex", "regexall", "startswith"],
      "requires_arn_prefix": true,
      "candidates": [
        "\"arn:aws:s3:::tf-mut\"",
        "\"arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000\""
      ]
    }
  ],
  "next_typed": {
    "string": ["\"tfmut-repair\"", "\"tfmut-repair-next\""],
    "number": ["2", "3"],
    "bool": ["false", "true"]
  }
}`

	// Updated only by an intentional edit of the table above. The corpus
	// harness verifies this digest and writes the table to the measurement
	// artefact before it executes a scenario.
	repairCandidateTableDigest = "6a24c4f659f8e9cbaeb41469f6d5e33e270b16123400bca3adc72303d6cc9fbf"

	repairOutcomeVerified    = "verified"
	repairOutcomeRefuted     = "refuted"
	repairOutcomeRefused     = "refused"
	repairOutcomeNoCandidate = "no-candidate"
	repairOutcomeUnmeasured  = "unmeasured"

	repairInputVerified    = "verified"
	repairInputRefuted     = "refuted"
	repairInputNoCandidate = "no-candidate"
	repairInputUnmeasured  = "unmeasured"

	repairMappingMapped     = "mapped"
	repairMappingUnmappable = "unmappable"
)

var (
	errRepairTableDigest        = errors.New("repair candidate table does not match its pinned digest")
	errRepairNoCharacterisation = errors.New("todos report carries no characterisation block")
)

type repairCandidateTable struct {
	Version   int                   `json:"version"`
	Rules     []repairCandidateRule `json:"rules"`
	NextTyped map[string][]string   `json:"next_typed"`
}

type repairCandidateRule struct {
	Name              string   `json:"name"`
	Functions         []string `json:"functions"`
	RequiresARNPrefix bool     `json:"requires_arn_prefix"`
	Candidates        []string `json:"candidates"`
}

type repairCandidate struct {
	Rule       string
	Candidates []string
}

type repairInputDefinition struct {
	Name             string
	Type             string
	HasDefault       bool
	ValidationRanges []report.Range
}

type repairDiagnostic struct {
	Severity string                 `json:"severity"`
	Summary  string                 `json:"summary"`
	Detail   string                 `json:"detail"`
	Range    *repairDiagnosticRange `json:"range"`
	Snippet  *repairSnippet         `json:"snippet"`
}

type repairDiagnosticRange struct {
	Filename string                   `json:"filename"`
	Start    repairDiagnosticPosition `json:"start"`
	End      repairDiagnosticPosition `json:"end"`
}

type repairDiagnosticPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Byte   int `json:"byte"`
}

type repairSnippet struct {
	Values []repairSnippetValue `json:"values"`
}

type repairSnippetValue struct {
	Traversal string `json:"traversal"`
	Statement string `json:"statement"`
}

type repairInputResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type repairModuleRow struct {
	Module        string              `json:"module"`
	Ref           string              `json:"ref"`
	Opportunities []string            `json:"opportunities"`
	Outcome       string              `json:"outcome"`
	Inputs        []repairInputResult `json:"inputs"`
	FailedInput   string              `json:"failed_input,omitempty"`
	Mapping       string              `json:"mapping,omitempty"`
	Attempts      int                 `json:"attempts"`
	Invocations   int                 `json:"invocations"`
	WallTimeMS    int64               `json:"wall_time_ms"`
	Reason        string              `json:"reason,omitempty"`
}

type repairTotals struct {
	Opportunities            int      `json:"opportunities"`
	ModulesWithOpportunities int      `json:"modules_with_opportunities"`
	Cohort                   int      `json:"cohort_size"`
	Verified                 int      `json:"verified"`
	Refuted                  int      `json:"refuted"`
	Refused                  int      `json:"refused"`
	NoCandidate              int      `json:"no_candidate"`
	Unmeasured               int      `json:"unmeasured"`
	VerifiedRate             *float64 `json:"verified_rate"`
}

type repairMeasurement struct {
	TableDigest string               `json:"candidate_table_sha256"`
	Table       repairCandidateTable `json:"candidate_table"`
	Modules     []repairModuleRow    `json:"modules"`
	Totals      repairTotals         `json:"totals"`
	Decision    string               `json:"decision"`
}

type repairAttempt struct {
	Report      report.Report
	Diagnostics []repairDiagnostic
	Err         error
}

type repairRecorder struct {
	logDir string
	binary string
}

func TestTheRepairCandidateTableIsPinnedAndUsesTodoConstraints(t *testing.T) {
	t.Parallel()

	table, err := loadRepairCandidateTable()
	if err != nil {
		t.Fatal(err)
	}

	cidr := report.Todo{Constraint: "can(cidrnetmask(var.cidr))"}
	selected, found := selectRepairCandidate(table, cidr,
		repairInputDefinition{Name: repairVariableCIDR, Type: repairTypeString})
	if !found || selected.Rule != "cidr-functions" || selected.Candidates[0] != `"10.0.0.0/16"` {
		t.Fatalf("CIDR selection = %+v, %t", selected, found)
	}

	arn := report.Todo{Constraint: `can(regex("^arn:aws:s3:::", var.bucket_arn))`}
	selected, found = selectRepairCandidate(table, arn,
		repairInputDefinition{Name: "bucket_arn", Type: repairTypeString})
	if !found || selected.Rule != "arn-functions" {
		t.Fatalf("ARN selection = %+v, %t", selected, found)
	}

	plainRegex := report.Todo{Constraint: `can(regex("^[0-9]{12}$", var.account_id))`}
	if selected, found = selectRepairCandidate(table, plainRegex,
		repairInputDefinition{Name: "account_id", Type: repairTypeString}); found {
		t.Fatalf("a non-ARN regex selected %+v", selected)
	}

	unconstrained := report.Todo{}
	selected, found = selectRepairCandidate(table, unconstrained,
		repairInputDefinition{Name: "name", Type: repairTypeString})
	if !found || selected.Rule != "next-typed:string" || selected.Candidates[0] != `"tfmut-repair"` {
		t.Fatalf("next typed selection = %+v, %t", selected, found)
	}

	withDefault := repairInputDefinition{Name: "name", Type: repairTypeString, HasDefault: true}
	if selected, found = selectRepairCandidate(table, unconstrained, withDefault); found {
		t.Fatalf("a defaulted input selected %+v", selected)
	}
}

func TestTheRepairPrototypeMapsOnlyOneStructuredInputAndRetriesOnce(t *testing.T) {
	t.Parallel()

	inputs := map[string]repairInputDefinition{
		repairVariableCIDR: {
			Name: repairVariableCIDR,
			ValidationRanges: []report.Range{{
				File:  "main.tf",
				Start: report.Position{Line: 4, Column: 21},
				End:   report.Position{Line: 4, Column: 47},
			}},
		},
		repairVariableRegion: {
			Name: repairVariableRegion,
			ValidationRanges: []report.Range{{
				File:  "main.tf",
				Start: report.Position{Line: 10, Column: 21},
				End:   report.Position{Line: 10, Column: 45},
			}},
		},
	}

	cases := []struct {
		name        string
		diagnostics []repairDiagnostic
		want        string
		mapped      bool
	}{
		{name: "zero mappings", diagnostics: []repairDiagnostic{{
			Summary: "var.cidr appears only in prose",
			Detail:  "the prose names var.cidr and must not be parsed",
		}}},
		{name: "one traversal", diagnostics: []repairDiagnostic{{
			Snippet: &repairSnippet{Values: []repairSnippetValue{{Traversal: repairTraversalCIDR}}},
		}}, want: repairVariableCIDR, mapped: true},
		{name: "one validation range", diagnostics: []repairDiagnostic{{
			Range: diagnosticRange("main.tf", 10, 30),
		}}, want: repairVariableRegion, mapped: true},
		{name: "child module file with the root basename", diagnostics: []repairDiagnostic{{
			Range: diagnosticRange("modules/net/main.tf", 10, 30),
		}}},
		{name: "two traversals", diagnostics: []repairDiagnostic{{
			Snippet: &repairSnippet{Values: []repairSnippetValue{
				{Traversal: repairTraversalCIDR}, {Traversal: "var.region"},
			}},
		}}},
		{name: "traversal and range conflict", diagnostics: []repairDiagnostic{{
			Range:   diagnosticRange("main.tf", 10, 30),
			Snippet: &repairSnippet{Values: []repairSnippetValue{{Traversal: repairTraversalCIDR}}},
		}}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, mapped := mapRepairInput(test.diagnostics, inputs)
			if got != test.want || mapped != test.mapped {
				t.Fatalf("mapping = (%q, %t), want (%q, %t)", got, mapped, test.want, test.mapped)
			}

			retry, allowed := retryRepairInput(1, test.diagnostics, inputs)
			if retry != test.want || allowed != test.mapped {
				t.Fatalf("first retry = (%q, %t), want (%q, %t)", retry, allowed, test.want, test.mapped)
			}

			if retry, allowed = retryRepairInput(2, test.diagnostics, inputs); allowed || retry != "" {
				t.Fatalf("a second retry was permitted: (%q, %t)", retry, allowed)
			}
		})
	}
}

func TestTheRepairDecisionRuleIsTotalOverTheCohort(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		totals repairTotals
		want   string
	}{
		{name: "empty cohort", totals: repairTotals{}, want: "insufficient"},
		{name: "one quarter verifies", totals: repairTotals{Cohort: 4, Verified: 1}, want: "positive"},
		{name: "under one quarter verifies", totals: repairTotals{Cohort: 5, Verified: 1}, want: "amendment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := repairDecision(test.totals); got != test.want {
				t.Fatalf("repairDecision(%+v) = %q, want %q", test.totals, got, test.want)
			}
		})
	}
}

func TestTheRepairPrototypeUsesTodosForLookupAndStructuredFieldsForMapping(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, repairDiagnosticFixture)
	request := characteriseRequest(t, module)
	recorder := newRepairRecorder(t)
	request.Common = repairCommon(request.Common, recorder.binary)

	todos := listRepairTodos(t, request.Common)
	if len(todos) != 1 {
		t.Fatalf("todos = %d, want one", len(todos))
	}

	todo := todos[0]
	if todo.Variable != repairVariableCIDR || todo.Constraint != "can(cidrnetmask(var.cidr))" {
		t.Fatalf("the judgement point did not publish the lookup source: %+v", todo)
	}

	table, err := loadRepairCandidateTable()
	if err != nil {
		t.Fatal(err)
	}

	definitions := repairInputDefinitions(t, module, todos)
	selected, found := selectRepairCandidate(table, todo, definitions[todo.Variable])
	if !found || selected.Rule != "cidr-functions" {
		t.Fatalf("candidate selection = %+v, %t", selected, found)
	}

	// Force the review's diagnostic shape. This value is not a table lookup;
	// the table lookup above has already been proved to come from todos.
	attempt := runRepairAttempt(t.Context(), t, request, recorder,
		map[string]string{todo.ID: `"tf-mut"`})
	if attempt.Err != nil {
		t.Fatalf("staged failed answer: %v", attempt.Err)
	}

	if !repairAttemptRefuted(attempt.Report) {
		t.Fatal("the invalid CIDR did not produce a refuted staged attempt")
	}

	if len(attempt.Diagnostics) == 0 {
		t.Fatal("the failed attempt published no structured diagnostic to the adapter")
	}

	mapped, ok := mapRepairInput(attempt.Diagnostics, definitions)
	if !ok || mapped != repairVariableCIDR {
		t.Fatalf("diagnostic mapping = (%q, %t), want cidr", mapped, ok)
	}

	foundTraversal := false
	foundRunRange := false

	for _, diagnostic := range attempt.Diagnostics {
		if diagnostic.Range != nil && strings.HasSuffix(
			filepath.ToSlash(diagnostic.Range.Filename), ".tftest.hcl",
		) {
			foundRunRange = true
		}

		if diagnostic.Snippet == nil {
			continue
		}

		for _, value := range diagnostic.Snippet.Values {
			if value.Traversal == repairTraversalCIDR {
				foundTraversal = true
			}
		}
	}

	if !foundTraversal {
		t.Fatal("the runtime diagnostic carries no var.cidr traversal")
	}

	if !foundRunRange {
		t.Fatal("the runtime diagnostic range does not point at the generated run block")
	}

	if strings.HasSuffix(filepath.ToSlash(todo.Range.File), ".tftest.hcl") {
		t.Fatalf("the todos range points at a test literal rather than the module constraint: %+v", todo.Range)
	}
}

func TestARepairPrototypeGateRefusalInvokesOnlyVersion(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, repairDiagnosticFixture)

	writeFile(t, filepath.Join(module, "effects.tf"), `resource "terraform_data" "effect" {
  provisioner "local-exec" {
    command = "exit 99"
  }
}
`)

	request := characteriseRequest(t, module)
	recorder := newRepairRecorder(t)
	request.Common = repairCommon(request.Common, recorder.binary)

	todos := listRepairTodos(t, request.Common)
	if len(todos) != 1 {
		t.Fatalf("todos = %d, want one", len(todos))
	}

	attempt := runRepairAttempt(t.Context(), t, request, recorder,
		map[string]string{todos[0].ID: `"10.0.0.0/16"`})
	if !errors.Is(attempt.Err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want effects-gate refusal", attempt.Err)
	}

	invocations := recorder.invocations(t)
	if len(invocations) != 1 || invocations[0] != versionInvocation {
		t.Fatalf("invocations = %v, want exactly [version]", invocations)
	}
}

func TestASecretOnlyInARepairFailedAttemptReachesNoPublishedArtefact(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedSensitiveAnswerFixture)
	request := characteriseRequest(t, module)
	recorder := newRepairRecorder(t)
	request.Common = repairCommon(request.Common, recorder.binary)

	todos := listRepairTodos(t, request.Common)
	if len(todos) != 1 {
		t.Fatalf("todos = %d, want one", len(todos))
	}

	//nolint:gosec // planted fixture secret; the assertion proves it is unpublished.
	const secret = `"tfmut-prototype-secret"`

	attempt := runRepairAttempt(t.Context(), t, request, recorder,
		map[string]string{todos[0].ID: secret})
	if attempt.Err != nil {
		t.Fatalf("staged failed answer: %v", attempt.Err)
	}

	if !repairAttemptRefuted(attempt.Report) {
		t.Fatal("the invalid sensitive answer did not produce a failed attempt")
	}

	definitions := repairInputDefinitions(t, module, todos)
	failed, mapped := mapRepairInput(attempt.Diagnostics, definitions)
	mapping := repairMappingUnmappable
	if mapped {
		mapping = repairMappingMapped
	}

	row := repairModuleRow{Module: "sensitive-fixture", Opportunities: []string{todos[0].Variable}, Attempts: 1}
	setRefutedRepairRow(&row, row.Opportunities, failed, mapping)
	table, err := loadRepairCandidateTable()
	if err != nil {
		t.Fatal(err)
	}

	measurement := repairMeasurement{
		TableDigest: repairCandidateTableDigest,
		Table:       table,
		Modules:     []repairModuleRow{row},
		Totals:      summariseRepairRows([]repairModuleRow{row}),
		Decision:    "fixture",
	}

	published := strings.Builder{}
	encoded, err := json.Marshal(measurement)
	if err != nil {
		t.Fatalf("encoding prototype publication: %v", err)
	}
	published.Write(encoded)

	if err := report.WriteJSON(&published, attempt.Report); err != nil {
		t.Fatalf("encoding failed-attempt report: %v", err)
	}

	if err := report.WriteTerminal(&published, attempt.Report); err != nil {
		t.Fatalf("rendering failed-attempt report: %v", err)
	}

	for _, file := range attempt.Report.Characterisation.Files {
		published.WriteString(file.Content)
	}

	published.WriteString(strings.Join(recorder.invocations(t), "\n"))

	if strings.Contains(published.String(), strings.Trim(secret, `"`)) {
		t.Fatal("the failed attempt's secret reached a published artefact")
	}
}

func loadRepairCandidateTable() (repairCandidateTable, error) {
	sum := sha256.Sum256([]byte(repairCandidateTableJSON))
	if hex.EncodeToString(sum[:]) != repairCandidateTableDigest {
		return repairCandidateTable{}, fmt.Errorf(
			"%w: got %s, want %s", errRepairTableDigest,
			hex.EncodeToString(sum[:]), repairCandidateTableDigest,
		)
	}

	var table repairCandidateTable
	if err := json.Unmarshal([]byte(repairCandidateTableJSON), &table); err != nil {
		return repairCandidateTable{}, fmt.Errorf("decoding repair candidate table: %w", err)
	}

	return table, nil
}

func selectRepairCandidate(
	table repairCandidateTable,
	todo report.Todo,
	definition repairInputDefinition,
) (repairCandidate, bool) {
	if todo.Constraint == "" {
		if definition.HasDefault {
			return repairCandidate{}, false
		}

		candidates := table.NextTyped[strings.TrimSpace(definition.Type)]
		if len(candidates) == 0 {
			return repairCandidate{}, false
		}

		return repairCandidate{
			Rule:       "next-typed:" + strings.TrimSpace(definition.Type),
			Candidates: slices.Clone(candidates),
		}, true
	}

	functions, arnPrefix, ok := repairConstraintKeys(todo.Constraint)
	if !ok {
		return repairCandidate{}, false
	}

	matches := []repairCandidateRule{}

	for _, rule := range table.Rules {
		if rule.RequiresARNPrefix && !arnPrefix {
			continue
		}

		if !intersects(functions, rule.Functions) {
			continue
		}

		matches = append(matches, rule)
	}

	if len(matches) != 1 || len(matches[0].Candidates) == 0 {
		return repairCandidate{}, false
	}

	return repairCandidate{
		Rule:       matches[0].Name,
		Candidates: slices.Clone(matches[0].Candidates),
	}, true
}

func repairConstraintKeys(
	constraint string,
) (functions map[string]bool, arnPrefix, parsed bool) {
	expression, diagnostics := hclsyntax.ParseExpression(
		[]byte(constraint), "constraint", hcl.InitialPos,
	)
	if diagnostics.HasErrors() {
		return nil, false, false
	}

	functions = map[string]bool{}

	_ = hclsyntax.VisitAll(expression, func(node hclsyntax.Node) hcl.Diagnostics {
		if call, ok := node.(*hclsyntax.FunctionCallExpr); ok {
			functions[call.Name] = true
		}

		expression, ok := node.(hclsyntax.Expression)
		if !ok {
			return nil
		}

		value, valueDiagnostics := expression.Value(nil)
		if valueDiagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
			return nil
		}

		if strings.Contains(value.AsString(), "arn:") {
			arnPrefix = true
		}

		return nil
	})

	return functions, arnPrefix, true
}

func intersects(found map[string]bool, wanted []string) bool {
	for _, name := range wanted {
		if found[name] {
			return true
		}
	}

	return false
}

func mapRepairInput(
	diagnostics []repairDiagnostic,
	inputs map[string]repairInputDefinition,
) (string, bool) {
	mapped := map[string]bool{}

	for _, diagnostic := range diagnostics {
		mapRepairTraversals(mapped, diagnostic, inputs)
		mapRepairRange(mapped, diagnostic, inputs)
	}

	if len(mapped) != 1 {
		return "", false
	}

	for name := range mapped {
		return name, true
	}

	return "", false
}

func mapRepairTraversals(
	mapped map[string]bool,
	diagnostic repairDiagnostic,
	inputs map[string]repairInputDefinition,
) {
	if diagnostic.Snippet == nil {
		return
	}

	for _, value := range diagnostic.Snippet.Values {
		name, parsed := traversalInput(value.Traversal)
		if _, known := inputs[name]; parsed && known {
			mapped[name] = true
		}
	}
}

func mapRepairRange(
	mapped map[string]bool,
	diagnostic repairDiagnostic,
	inputs map[string]repairInputDefinition,
) {
	if diagnostic.Range == nil {
		return
	}

	for name, input := range inputs {
		for _, validation := range input.ValidationRanges {
			if diagnosticInsideRange(*diagnostic.Range, validation) {
				mapped[name] = true
			}
		}
	}
}

func traversalInput(traversal string) (string, bool) {
	parsed, diagnostics := hclsyntax.ParseTraversalAbs(
		[]byte(traversal), "diagnostic", hcl.InitialPos,
	)
	if diagnostics.HasErrors() || len(parsed) < 2 || parsed.RootName() != "var" {
		return "", false
	}

	attribute, ok := parsed[1].(hcl.TraverseAttr)
	if !ok || attribute.Name == "" {
		return "", false
	}

	return attribute.Name, true
}

func diagnosticInsideRange(diagnostic repairDiagnosticRange, validation report.Range) bool {
	if !sameRepairFile(diagnostic.Filename, validation.File) {
		return false
	}

	return positionAtOrAfter(diagnostic.Start.Line, diagnostic.Start.Column, validation.Start) &&
		positionAtOrBefore(diagnostic.End.Line, diagnostic.End.Column, validation.End)
}

func sameRepairFile(left, right string) bool {
	return filepath.ToSlash(filepath.Clean(left)) == filepath.ToSlash(filepath.Clean(right))
}

func positionAtOrAfter(line, column int, start report.Position) bool {
	return line > start.Line || line == start.Line && column >= start.Column
}

func positionAtOrBefore(line, column int, end report.Position) bool {
	return line < end.Line || line == end.Line && column <= end.Column
}

func retryRepairInput(
	completedAttempts int,
	diagnostics []repairDiagnostic,
	inputs map[string]repairInputDefinition,
) (string, bool) {
	if completedAttempts != 1 {
		return "", false
	}

	return mapRepairInput(diagnostics, inputs)
}

func diagnosticRange(file string, line, column int) *repairDiagnosticRange {
	return &repairDiagnosticRange{
		Filename: file,
		Start:    repairDiagnosticPosition{Line: line, Column: column},
		End:      repairDiagnosticPosition{Line: line, Column: column + 1},
	}
}

func repairInputDefinitions(
	t *testing.T,
	moduleDir string,
	todos []report.Todo,
) map[string]repairInputDefinition {
	t.Helper()

	configuration, err := discovery.Discover(moduleDir, engine.DefaultTestDirectory)
	if err != nil {
		t.Fatalf("discovering repair inputs: %v", err)
	}

	root, found := configuration.ModuleByDir(configuration.ModuleDir)
	if !found {
		t.Fatal("the repair module has no root module")
	}

	wanted := map[string]bool{}
	for _, todo := range todos {
		wanted[todo.Variable] = true
	}

	definitions := map[string]repairInputDefinition{}

	for _, variable := range root.Variables {
		if !wanted[variable.Name] {
			continue
		}

		definition := repairInputDefinition{Name: variable.Name}

		for _, attribute := range variable.Attributes {
			switch attribute.Name {
			case "default":
				definition.HasDefault = true
			case "type":
				definition.Type = repairExpressionSource(t,
					attribute.Expr.Range().Filename, attribute.Expr.Range())
			default:
				// No other variable attribute informs this prototype.
			}
		}

		for _, validation := range variable.Validations {
			definition.ValidationRanges = append(definition.ValidationRanges,
				projectRepairRange(t, configuration.ModuleDir, validation.Range))
		}

		definitions[variable.Name] = definition
	}

	return definitions
}

func repairExpressionSource(t *testing.T, file string, sourceRange hcl.Range) string {
	t.Helper()

	content, err := os.ReadFile(file) //nolint:gosec // a discovered corpus module file.
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}

	if sourceRange.Start.Byte < 0 || sourceRange.End.Byte > len(content) ||
		sourceRange.Start.Byte >= sourceRange.End.Byte {
		t.Fatalf("invalid expression range in %s: %+v", file, sourceRange)
	}

	return strings.TrimSpace(string(content[sourceRange.Start.Byte:sourceRange.End.Byte]))
}

func projectRepairRange(t *testing.T, moduleDir string, sourceRange hcl.Range) report.Range {
	t.Helper()

	file, err := filepath.Rel(moduleDir, sourceRange.Filename)
	if err != nil {
		t.Fatalf("making validation range relative: %v", err)
	}

	return report.Range{
		File:  filepath.ToSlash(file),
		Start: report.Position{Line: sourceRange.Start.Line, Column: sourceRange.Start.Column},
		End:   report.Position{Line: sourceRange.End.Line, Column: sourceRange.End.Column},
	}
}

func listRepairTodos(t *testing.T, common engine.Common) []report.Todo {
	t.Helper()

	todos, err := repairTodos(t.Context(), common)
	if err != nil {
		t.Fatalf("listing repair opportunities: %v", err)
	}

	return todos
}

func repairTodos(ctx context.Context, common engine.Common) ([]report.Todo, error) {
	result, err := engine.Run(ctx, &engine.TodosRequest{Common: common})
	if err != nil {
		return nil, err
	}

	if result.Characterisation == nil {
		return nil, errRepairNoCharacterisation
	}

	open := []report.Todo{}
	for _, todo := range result.Characterisation.Todos {
		if todo.Status == report.TodoOpen {
			open = append(open, todo)
		}
	}

	return open, nil
}

func runRepairAttempt(
	ctx context.Context,
	t *testing.T,
	request engine.CharacteriseRequest,
	recorder repairRecorder,
	answers map[string]string,
) repairAttempt {
	t.Helper()

	before := recorder.streams(t)
	request.Answers = renderRepairAnswers(answers)

	result, err := engine.Run(ctx, &request)
	return repairAttempt{
		Report: result, Diagnostics: recorder.diagnosticsSince(t, before), Err: err,
	}
}

func renderRepairAnswers(answers map[string]string) []string {
	identifiers := make([]string, 0, len(answers))
	for identifier := range answers {
		identifiers = append(identifiers, identifier)
	}

	slices.Sort(identifiers)

	rendered := make([]string, 0, len(answers))
	for _, identifier := range identifiers {
		rendered = append(rendered, identifier+"="+answers[identifier])
	}

	return rendered
}

func repairAttemptRefuted(result report.Report) bool {
	if result.Characterisation == nil {
		return false
	}

	for _, todo := range result.Characterisation.Todos {
		if todo.Status == report.TodoRejected {
			return true
		}
	}

	return false
}

func repairCommon(common engine.Common, binary string) engine.Common {
	common.TerraformBinary = binary
	common.Env = append(common.Env,
		"GIT_DIR=", "GIT_WORK_TREE=", "BASH_ENV=", "ENV=", "CDPATH=",
	)

	return common
}

func newRepairRecorder(t *testing.T) repairRecorder {
	t.Helper()

	realTerraform, err := exec.LookPath(engine.DefaultTerraformBinary)
	if err != nil {
		t.Skipf("terraform unavailable: %v", err)
	}

	root := t.TempDir()
	wrapper := filepath.Join(root, "terraform-recording")
	log := filepath.Join(root, "invocations")
	count := filepath.Join(root, "stream-count")
	streams := filepath.Join(root, "streams")

	if err := os.MkdirAll(streams, 0o700); err != nil {
		t.Fatalf("creating stream directory: %v", err)
	}

	script := fmt.Sprintf(`#!/bin/sh
set -u
command_name=""
for argument in "$@"; do
  case "$argument" in
    -chdir=*) ;;
    *) command_name="$argument"; break ;;
  esac
done
printf '%%s\n' "$command_name" >> %s
if [ "$command_name" != "test" ]; then
  exec %s "$@"
fi
number=0
if [ -f %s ]; then
  number=$(cat %s)
fi
number=$((number + 1))
printf '%%s\n' "$number" > %s
stream=$(printf '%s/stream-%%03d.jsonl' "$number")
%s "$@" > "$stream"
status=$?
cat "$stream"
if [ "$status" -eq 0 ]; then
  rm -f "$stream"
fi
exit "$status"
`, shellQuote(log), shellQuote(realTerraform), shellQuote(count), shellQuote(count),
		shellQuote(count), streams, shellQuote(realTerraform))

	if err := os.WriteFile(wrapper, []byte(script), 0o600); err != nil {
		t.Fatalf("writing Terraform recorder: %v", err)
	}

	if err := os.Chmod(wrapper, 0o700); err != nil { //nolint:gosec // A test-owned command wrapper must execute.
		t.Fatalf("making Terraform recorder executable: %v", err)
	}

	return repairRecorder{logDir: root, binary: wrapper}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (r repairRecorder) invocations(t *testing.T) []string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(r.logDir, "invocations"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		t.Fatalf("reading invocation log: %v", err)
	}

	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "\n")
}

func (r repairRecorder) streams(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(r.logDir, "streams"))
	if err != nil {
		t.Fatalf("reading diagnostic streams: %v", err)
	}

	return len(entries)
}

func (r repairRecorder) diagnosticsSince(t *testing.T, before int) []repairDiagnostic {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(r.logDir, "streams"))
	if err != nil {
		t.Fatalf("reading diagnostic streams: %v", err)
	}

	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})

	diagnostics := []repairDiagnostic{}

	for _, entry := range entries[before:] {
		path := filepath.Join(r.logDir, "streams", entry.Name())
		file, openErr := os.Open(path) //nolint:gosec // test-owned path.
		if openErr != nil {
			t.Fatalf("opening %s: %v", path, openErr)
		}

		decoder := json.NewDecoder(file)
		for {
			var message struct {
				Diagnostic *repairDiagnostic `json:"diagnostic"`
			}

			decodeErr := decoder.Decode(&message)
			if errors.Is(decodeErr, io.EOF) {
				break
			}

			if decodeErr != nil {
				_ = file.Close()
				t.Fatalf("decoding %s: %v", path, decodeErr)
			}

			if message.Diagnostic != nil {
				diagnostics = append(diagnostics, *message.Diagnostic)
			}
		}

		if closeErr := file.Close(); closeErr != nil {
			t.Fatalf("closing %s: %v", path, closeErr)
		}
	}

	return diagnostics
}

func setRefutedRepairRow(row *repairModuleRow, names []string, failed, mapping string) {
	row.Outcome = repairOutcomeRefuted
	row.FailedInput = failed
	row.Mapping = mapping
	row.Inputs = repairStatuses(names, failed, repairInputUnmeasured)
}

func repairStatuses(names []string, failed, other string) []repairInputResult {
	inputs := make([]repairInputResult, 0, len(names))

	for _, name := range names {
		status := other
		if failed != "" && name == failed {
			status = repairInputRefuted
		}

		inputs = append(inputs, repairInputResult{Name: name, Status: status})
	}

	return inputs
}

func repairDecision(totals repairTotals) string {
	switch {
	case totals.Cohort == 0:
		return "insufficient"
	case totals.Verified*4 >= totals.Cohort:
		return "positive"
	default:
		return "amendment"
	}
}

func summariseRepairRows(rows []repairModuleRow) repairTotals {
	totals := repairTotals{}

	for _, row := range rows {
		totals.ModulesWithOpportunities++
		totals.Opportunities += len(row.Opportunities)

		switch row.Outcome {
		case repairOutcomeVerified:
			totals.Verified++
		case repairOutcomeRefuted:
			totals.Refuted++
		case repairOutcomeRefused:
			totals.Refused++
		case repairOutcomeNoCandidate:
			totals.NoCandidate++
		case repairOutcomeUnmeasured:
			totals.Unmeasured++
		default:
			// An unfinished row contributes only its population counts.
		}
	}

	totals.Cohort = totals.Verified + totals.Refuted
	if totals.Cohort > 0 {
		rate := float64(totals.Verified) / float64(totals.Cohort)
		totals.VerifiedRate = &rate
	}

	return totals
}
