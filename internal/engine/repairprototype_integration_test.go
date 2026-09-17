//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

const repairPrototypeOutput = "../../.artifacts/measurement/m5-repair-prototype.json"

var repairOpportunityCounts = map[string]int{ //nolint:gochecknoglobals // the census-pinned population.
	"aws-platform-starter":             4,
	"genai-idp-terraform":              4,
	"aws-terraform-infrastructure":     1,
	"eks-vulnerable-infra":             1,
	"psoxy":                            1,
	"server-terraform":                 1,
	"serverless-architecture-patterns": 1,
	"terraform-datadog-users":          1,
}

// TestTheRepairPrototypeOverThePinnedOpportunities runs the throwaway table
// over the 14 opportunities #156 found. It publishes the table before the
// first scenario, then every module row as it completes. The recipe's opt-in
// licenses fetching the pinned archives; every staged scenario keeps both
// product safety opt-ins false.
//
//nolint:paralleltest // owns the one published measurement artefact.
func TestTheRepairPrototypeOverThePinnedOpportunities(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	table, err := loadRepairCandidateTable()
	if err != nil {
		t.Fatal(err)
	}

	measurement := repairMeasurement{
		TableDigest: repairCandidateTableDigest,
		Table:       table,
		Modules:     []repairModuleRow{},
		Totals:      repairTotals{},
		Decision:    "pending",
	}

	// This write precedes archive fetches, version probes and scenarios. An
	// interrupted measurement therefore still publishes the exact table whose
	// candidates any completed row used.
	publishRepairMeasurement(t, measurement)

	corpus := loadBenchmarkCorpus(t)
	seen := map[string]bool{}

	for _, module := range corpus.Modules {
		want, selected := repairOpportunityCounts[module.Name]
		if !selected {
			continue
		}

		row := measureRepairModule(t.Context(), t, module, want)
		if row.Outcome == repairOutcomeRefused && row.Invocations != 1 {
			t.Fatalf("%s refusal made %d Terraform invocations, want version only",
				module.Name, row.Invocations)
		}

		measurement.Modules = append(measurement.Modules, row)
		seen[module.Name] = true

		measurement.Totals = summariseRepairRows(measurement.Modules)
		measurement.Decision = repairDecision(measurement.Totals)
		publishRepairMeasurement(t, measurement)
	}

	if len(seen) != len(repairOpportunityCounts) {
		t.Fatalf("measured %d repair modules, want %d", len(seen), len(repairOpportunityCounts))
	}

	measurement.Totals = summariseRepairRows(measurement.Modules)
	measurement.Decision = repairDecision(measurement.Totals)
	publishRepairMeasurement(t, measurement)

	if measurement.Totals.Opportunities != 14 || measurement.Totals.ModulesWithOpportunities != 8 {
		t.Fatalf("prototype population = %+v, want 14 opportunities over 8 modules", measurement.Totals)
	}

	if measurement.Totals.Cohort != measurement.Totals.Verified+measurement.Totals.Refuted {
		t.Fatalf("cohort includes an out-of-cohort outcome: %+v", measurement.Totals)
	}

	t.Logf("repair prototype: cohort %d; verified %d; refuted %d; refused %d; no-candidate %d; unmeasured %d; decision %s",
		measurement.Totals.Cohort, measurement.Totals.Verified,
		measurement.Totals.Refuted, measurement.Totals.Refused,
		measurement.Totals.NoCandidate, measurement.Totals.Unmeasured,
		measurement.Decision)
}

func measureRepairModule(
	ctx context.Context,
	t *testing.T,
	module benchmarkModule,
	wantOpportunities int,
) (row repairModuleRow) {
	t.Helper()

	root, _, fetchErr := fetchPinnedRepositoryRetry(ctx, t, module)
	if fetchErr != nil {
		return repairModuleRow{
			Module: module.Name, Ref: module.Commit,
			Outcome: repairOutcomeUnmeasured, Reason: "pinned archive fetch failed",
		}
	}

	started := time.Now()
	defer func() { row.WallTimeMS = elapsedMilliseconds(started) }()

	moduleDir := filepath.Join(root, filepath.FromSlash(module.ModuleSubdir))
	recorder := newRepairRecorder(t)
	request := characteriseRequest(t, moduleDir)
	request.Common = repairCommon(request.Common, recorder.binary)

	todos, listErr := repairTodos(ctx, request.Common)
	if listErr != nil {
		return repairModuleRow{
			Module: module.Name, Ref: module.Commit,
			Outcome: repairOutcomeUnmeasured, Invocations: len(recorder.invocations(t)),
			Reason: "static opportunity listing failed",
		}
	}

	if len(todos) != wantOpportunities {
		return repairModuleRow{
			Module: module.Name, Ref: module.Commit,
			Opportunities: repairTodoNames(todos), Outcome: repairOutcomeUnmeasured,
			Invocations: len(recorder.invocations(t)),
			Reason: fmt.Sprintf("opportunity count changed: got %d, census pinned %d",
				len(todos), wantOpportunities),
		}
	}

	definitions := repairInputDefinitions(t, moduleDir, todos)
	table, err := loadRepairCandidateTable()
	if err != nil {
		t.Fatal(err)
	}

	row = executeRepairScenario(ctx, t, module, request, recorder, table, todos, definitions)

	return row
}

func executeRepairScenario(
	ctx context.Context,
	t *testing.T,
	module benchmarkModule,
	request engine.CharacteriseRequest,
	recorder repairRecorder,
	table repairCandidateTable,
	todos []report.Todo,
	definitions map[string]repairInputDefinition,
) repairModuleRow {
	t.Helper()

	names := repairTodoNames(todos)
	row := repairModuleRow{
		Module: module.Name, Ref: module.Commit, Opportunities: names,
		Inputs: make([]repairInputResult, 0, len(todos)),
	}
	answers := map[string]string{}
	candidates := map[string]repairCandidate{}
	identifiers := map[string]string{}
	allCandidates := true

	for _, todo := range todos {
		selected, found := selectRepairCandidate(table, todo, definitions[todo.Variable])
		if !found {
			allCandidates = false
			row.Inputs = append(row.Inputs,
				repairInputResult{Name: todo.Variable, Status: repairInputNoCandidate})

			continue
		}

		answers[todo.ID] = selected.Candidates[0]
		candidates[todo.Variable] = selected
		identifiers[todo.Variable] = todo.ID
		row.Inputs = append(row.Inputs,
			repairInputResult{Name: todo.Variable, Status: repairInputUnmeasured})
	}

	if !allCandidates {
		row.Outcome = repairOutcomeNoCandidate
		row.Invocations = len(recorder.invocations(t))

		return row
	}

	first := runRepairAttempt(ctx, t, request, recorder, answers)
	row.Attempts = 1

	if classifyRepairAttempt(&row, first, names, answers) {
		row.Invocations = len(recorder.invocations(t))

		return row
	}

	failed, mapped := retryRepairInput(1, first.Diagnostics, definitions)
	if !mapped || len(candidates[failed].Candidates) < 2 {
		mapping := repairMappingUnmappable
		if mapped {
			mapping = repairMappingMapped
		}

		setRefutedRepairRow(&row, names, failed, mapping)
		row.Invocations = len(recorder.invocations(t))

		return row
	}

	answers[identifiers[failed]] = candidates[failed].Candidates[1]
	second := runRepairAttempt(ctx, t, request, recorder, answers)
	row.Attempts = 2

	if classifyRepairAttempt(&row, second, names, answers) {
		row.Invocations = len(recorder.invocations(t))

		return row
	}

	failed, mapped = mapRepairInput(second.Diagnostics, definitions)
	mapping := repairMappingUnmappable
	if mapped {
		mapping = repairMappingMapped
	}

	setRefutedRepairRow(&row, names, failed, mapping)
	row.Invocations = len(recorder.invocations(t))

	return row
}

// classifyRepairAttempt handles the terminal branches. False means the staged
// scenario was red and the caller may consult structured diagnostics for the
// one permitted retry. Candidate values and diagnostic prose are never copied
// into the row.
func classifyRepairAttempt(
	row *repairModuleRow,
	attempt repairAttempt,
	names []string,
	answers map[string]string,
) bool {
	if attempt.Err != nil {
		switch {
		case errors.Is(attempt.Err, engine.ErrRealInfrastructure),
			errors.Is(attempt.Err, engine.ErrUnsandboxedEffects):
			row.Outcome = repairOutcomeRefused
			row.Reason = redactRepairValues(attempt.Err.Error(), answers)
		default:
			row.Outcome = repairOutcomeUnmeasured
			row.Reason = "operational failure in the staged characterise pipeline"
		}

		return true
	}

	if repairAttemptRefuted(attempt.Report) {
		return false
	}

	if attempt.Report.Characterisation == nil {
		row.Outcome = repairOutcomeUnmeasured
		row.Reason = "the staged characterise pipeline returned no result"

		return true
	}

	row.Outcome = repairOutcomeVerified
	row.Mapping = ""
	row.FailedInput = ""
	row.Inputs = repairStatuses(names, "", repairInputVerified)

	return true
}

func repairTodoNames(todos []report.Todo) []string {
	names := make([]string, 0, len(todos))
	for _, todo := range todos {
		names = append(names, todo.Variable)
	}

	slices.Sort(names)

	return names
}

func redactRepairValues(text string, answers map[string]string) string {
	redacted := text

	for _, answer := range answers {
		redacted = strings.ReplaceAll(redacted, answer, characterise.SensitiveWithheld)
		redacted = strings.ReplaceAll(redacted, strings.Trim(answer, `"`), characterise.SensitiveWithheld)
	}

	return redacted
}

func elapsedMilliseconds(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func publishRepairMeasurement(t *testing.T, measurement repairMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(repairPrototypeOutput), 0o750); err != nil {
		t.Fatalf("creating repair measurement directory: %v", err)
	}

	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding repair measurement: %v", err)
	}

	if err := os.WriteFile(repairPrototypeOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("publishing repair measurement: %v", err)
	}
}
