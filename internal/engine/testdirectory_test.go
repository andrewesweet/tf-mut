package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The suite's own helper module: a run block instantiates it from under the
// test directory, the way a setup fixture is shared out. A survivor in that
// helper says nothing about the module under test — the helper is part of the
// test's scaffolding — so no mutant may target it, while it stays in the
// closure so the run blocks that call it still materialise it.
const testdirHelperFixture = "testdir-helper"

// TestTheTestDirectorysHelperModulesAreNotMutated holds the default surface:
// files under the configured test directory are never mutated, in preview or
// in run, and the suite that calls the helper still passes.
func TestTheTestDirectorysHelperModulesAreNotMutated(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, testdirHelperFixture)
	before := treeDigest(t, module)

	preview, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	assertNoMutantsUnder(t, "tests/", preview)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		// A red suite aborts the run outright, so an error here is also how a
		// helper that failed to materialise would present.
		t.Fatalf("run: %v", err)
	}

	assertNoMutantsUnder(t, "tests/", result)

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants at all: the exclusion emptied the surface of the module too")
	}

	assertTreeUnchanged(t, module, before)
}

func TestTheModuleRootStaysMutableUnderARootColocatedTestDirectory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, testdirHelperFixture)
	before, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview with the default test directory: %v", err)
	}

	if len(before.Mutants) == 0 {
		t.Fatal("the default test directory produced no mutants")
	}

	if err := os.Rename(filepath.Join(module, "tests", "unit.tftest.hcl"),
		filepath.Join(module, "unit.tftest.hcl")); err != nil {
		t.Fatalf("moving the suite to the module root: %v", err)
	}

	request := previewRequest(t, module)
	request.TestDirectory = "."

	after, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("preview with a root-colocated test directory: %v", err)
	}

	if len(after.Mutants) != len(before.Mutants) {
		t.Fatalf("root-colocated suite changed population size: %d -> %d",
			len(before.Mutants), len(after.Mutants))
	}

	for index, mutant := range before.Mutants {
		if after.Mutants[index].ID != mutant.ID {
			t.Errorf("root-colocated suite changed mutant %s to %s",
				mutant.ID, after.Mutants[index].ID)
		}
	}
}

// assertNoMutantsUnder fails when any mutant's file sits below prefix.
func assertNoMutantsUnder(t *testing.T, prefix string, result report.Report) {
	t.Helper()

	for _, mutant := range result.Mutants {
		if strings.HasPrefix(mutant.Range.File, prefix) {
			t.Errorf("mutant %s (%s) targets %s, a file under the test directory",
				mutant.ID, mutant.Operator, mutant.Range.File)
		}
	}
}
