package engine_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// wholeMilestoneBase is the tree M5 started from: the commit the M5 spec
// review ran against, and the last commit before M5a's lifecycle operators
// landed. The whole-milestone check compares that tree's standard report with
// this tree's, so "before M5" is pinned by history, not by whatever the
// default branch happens to carry when the check runs.
const wholeMilestoneBase = "d57f2cd"

// TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone
// is the whole-M5 headline proof the M5 spec's exit table requires at M5d:
// the operator-matrix fixture's standard report from a binary built at M5's
// base tree and one built from this tree are identical under matched
// cache-off legs with `baseline.duration_ms` excluded — and the only
// difference the whole milestone is allowed beyond that exclusion is the
// schema-addition stamp `schema_version`, moved on from 2.3.0 by the pack
// work. The admitted stamp is whatever constant this tree publishes —
// `report.SchemaVersion`, 2.5.0 since the rebase onto the pack schema — and a
// tree that publishes no stamp change at all fails. With no pack selected,
// `origins` is absent from every mutant on either side, so the pack schema's
// added field carries no content here.
//
// Everything M5 shipped — the three lifecycle operators, the pack form
// operators, origins — is deep- or pack-tier, and neither enters a standard
// selection; the proof exists to catch the change that leaked into the
// default population anyway.
func TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	if !refExists(t, root, wholeMilestoneBase+"^{commit}") {
		t.Skipf("the M5 base tree %s is not present; the before leg needs its object",
			wholeMilestoneBase)
	}

	baseTree := t.TempDir()
	archiveCommit(t, root, wholeMilestoneBase, baseTree)

	// One module path for both legs: the report carries the module's absolute
	// path, so the legs must differ by the binary alone.
	module := filepath.Join(t.TempDir(), "module")

	copyFixtureInto(t, "operators", module)
	before := standardReport(t,
		buildBinary(t, baseTree, filepath.Join(t.TempDir(), "tf-mut-m5-base")), module)

	if err := os.RemoveAll(module); err != nil {
		t.Fatalf("resetting the module between legs: %v", err)
	}

	copyFixtureInto(t, "operators", module)
	after := standardReport(t,
		buildBinary(t, root, filepath.Join(t.TempDir(), "tf-mut-m5-head")), module)

	for _, document := range []map[string]any{before, after} {
		baseline, ok := document["baseline"].(map[string]any)
		if !ok {
			t.Fatal("the report carries no baseline object")
		}

		delete(baseline, "duration_ms")

		canonicaliseDiagnosticOrder(document)
	}

	// The authorised difference: the schema-addition stamp, exactly one step.
	if before["schema_version"] != "2.3.0" {
		t.Fatalf("the M5 base tree publishes schema %q, want 2.3.0", before["schema_version"])
	}

	if after["schema_version"] != report.SchemaVersion {
		t.Fatalf("this tree publishes schema %q, want %q",
			after["schema_version"], report.SchemaVersion)
	}

	if after["schema_version"] == before["schema_version"] {
		t.Fatalf("this tree publishes the M5 base tree's schema %q; the pack work moved it on",
			after["schema_version"])
	}

	delete(before, "schema_version")
	delete(after, "schema_version")

	// Origins is present exactly when a pack produced the bytes; no pack is
	// selected here, so no mutant on either side carries the field.
	for name, document := range map[string]map[string]any{"before": before, "after": after} {
		mutants, ok := document["mutants"].([]any)
		if !ok {
			t.Fatalf("the %s report carries no mutants list", name)
		}

		for index, entry := range mutants {
			mutant, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("%s mutant %d is not an object", name, index)
			}

			if _, present := mutant["origins"]; present {
				t.Fatalf("%s mutant %d carries origins with no pack selected", name, index)
			}

			if mutant["tier"] == "pack" {
				t.Fatalf("%s mutant %d is pack-tier with no pack selected", name, index)
			}
		}
	}

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the standard report of the matrix fixture moved across the whole milestone:\n  %s",
			firstDifference(before, after, "$"))
	}
}
