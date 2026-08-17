package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// M3d.1 (#51): the mutation-testing-elements reporter is an explicitly lossy
// interoperability adapter, per the C6 disposition: "schema version pinned,
// tf-mut's authoritative metrics embedded in the report's metadata and
// rendered above the viewer in the HTML page, the score disagreement tested
// rather than denied, and every claim of unchanged-dashboard-score reuse
// withdrawn."
//
// The mapping changes the viewer's computed score three ways: KilledByError
// maps to RuntimeError and leaves the numerator, Timeout counts as detected,
// and StructurallyUnassertable maps to Ignored and leaves the denominator.
// tf-mut's own metrics are the authoritative ones, and they travel inside the
// document's config member.

// MTESchemaVersion is the pinned mutation-testing-report-schema version,
// vendored at docs/schema/mutation-testing-report-2.0.0.json.
const MTESchemaVersion = "2.0.0"

// MTEVersion is the pinned mutation-testing-elements viewer release the HTML
// reporter embeds; its licence ships alongside the bundle.
const MTEVersion = "3.5.1"

// mteThresholds is the viewer's colour banding, not a gate.
const (
	mteThresholdHigh = 80
	mteThresholdLow  = 60
)

// WriteMTE renders the report as a mutation-testing-report-schema document.
func WriteMTE(writer io.Writer, value Report) error {
	document := mteDocument(value)

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encoding mutation-testing-elements report: %w", err)
	}

	return nil
}

// MTEComputedScore is the score the Stryker dashboard family computes from
// the mapped statuses: detected over detected-plus-undetected. It exists so
// the disagreement with the authoritative score is tested, not denied.
func MTEComputedScore(value Report) float64 {
	detected := 0
	undetected := 0

	for _, mutant := range value.Mutants {
		switch mteStatus(mutant) {
		case "Killed", "Timeout":
			detected++
		case "Survived", "NoCoverage":
			undetected++
		default:
		}
	}

	if detected+undetected == 0 {
		return 0
	}

	return float64(detected) / float64(detected+undetected)
}

// mteDocument builds the document: files keyed by closure-relative path with
// their current source text, mutants mapped per the declared-lossy table, and
// the authoritative metrics in config.
func mteDocument(value Report) map[string]any {
	files := map[string]any{}

	for _, mutant := range value.Mutants {
		path := mutant.Range.File

		entry, found := files[path].(map[string]any)
		if !found {
			entry = map[string]any{
				"language": "hcl",
				"source":   sourceOf(value, path),
				"mutants":  []any{},
			}
			files[path] = entry
		}

		mutants, ok := entry["mutants"].([]any)
		if !ok {
			mutants = []any{}
		}

		entry["mutants"] = append(mutants, mteMutant(mutant))
	}

	return map[string]any{
		"schemaVersion": MTESchemaVersion,
		"thresholds":    map[string]any{"high": mteThresholdHigh, "low": mteThresholdLow},
		"projectRoot":   value.ClosureRoot,
		"framework": map[string]any{
			"name":    sarifTool,
			"version": value.SchemaVersion,
		},
		"config": map[string]any{
			sarifTool: authoritativeMetrics(value),
		},
		"files": files,
	}
}

// authoritativeMetrics is the metadata block consumers must read for tf-mut's
// numbers: the mapping below this line is lossy by declaration.
func authoritativeMetrics(value Report) map[string]any {
	return map[string]any{
		"schema_version":  value.SchemaVersion,
		"mutation_score":  value.Metrics.MutationScore,
		"assertion_score": value.Metrics.AssertionScore,
		"reachability":    value.Metrics.Reachability,
		"incomplete":      value.Metrics.Incomplete,
		"scored":          value.Metrics.Scored,
		"counts":          value.Metrics.Counts,
		"note": "These are tf-mut's authoritative metrics. The viewer recomputes a score " +
			"from the mapped statuses and it will disagree: the mapping is a declared-lossy " +
			"interoperability adapter, not a second opinion.",
	}
}

// mteMutant maps one mutant into the closed status vocabulary.
func mteMutant(mutant Mutant) map[string]any {
	mapped := map[string]any{
		"id":          mutant.ID,
		"mutatorName": mutant.Operator,
		"status":      mteStatus(mutant),
		"location": map[string]any{
			"start": map[string]any{
				"line":   max(mutant.Range.Start.Line, 1),
				"column": max(mutant.Range.Start.Column, 1),
			},
			"end": map[string]any{
				"line":   max(mutant.Range.End.Line, 1),
				"column": max(mutant.Range.End.Column, 1),
			},
		},
	}

	if reason := mteStatusReason(mutant); reason != "" {
		mapped["statusReason"] = reason
	}

	if mutant.Verdict != nil && mutant.Verdict.Message != "" {
		mapped["description"] = mutant.Verdict.Message
	}

	return mapped
}

// mteStatus is the normative mapping (C6): KilledByError to RuntimeError,
// Invalid to CompileError, StructurallyUnassertable, Unobservable and Ignored
// to Ignored-with-reason, Timeout to Timeout, survivors to Survived.
func mteStatus(mutant Mutant) string {
	//nolint:exhaustive // the default arm is the Pending mapping.
	switch mutant.State {
	case Killed:
		return "Killed"
	case KilledByError:
		return "RuntimeError"
	case Invalid:
		return "CompileError"
	case StructurallyUnassertable, Unobservable, Ignored:
		return "Ignored"
	case Timeout:
		return "Timeout"
	case Survived:
		return "Survived"
	case NoCoverage:
		return "NoCoverage"
	default:
		// Pending, and anything a later milestone adds, ships as Pending —
		// the vocabulary's own unclassified state.
		return "Pending"
	}
}

// mteStatusReason carries the state and diagnosis across the lossy boundary.
func mteStatusReason(mutant Mutant) string {
	reason := string(mutant.State)

	if mutant.Verdict != nil && mutant.Verdict.Diagnosis != "" {
		reason += ": " + string(mutant.Verdict.Diagnosis)
	}

	return reason
}

// sourceOf reads the mutated file's current text, so the viewer can render
// the mutants in place. A file that cannot be read renders with an empty
// source rather than failing the report.
func sourceOf(value Report, relative string) string {
	root := value.ClosureRoot
	if root == "" {
		root = value.Module
	}

	content, err := os.ReadFile(filepath.Join(root, relative)) //nolint:gosec // report-owned module tree.
	if err != nil {
		return ""
	}

	return string(content)
}
