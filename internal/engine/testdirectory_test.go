package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// The suite's own helper module: a run block instantiates it from under the
// test directory, the way a setup fixture is shared out. A survivor in that
// helper says nothing about the module under test — the helper is part of the
// test's scaffolding — so no mutant may target it, while it stays in the
// closure so the run blocks that call it still materialise it.
const testdirHelperFixture = "testdir-helper"

const jsonTestSyntax = "json"

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

func TestACustomTestDirectoryInstallsItsHelperModules(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, testdirHelperFixture)
	if err := os.Rename(filepath.Join(module, "tests"), filepath.Join(module, "suite")); err != nil {
		t.Fatalf("moving the test directory: %v", err)
	}

	path := filepath.Join(module, "suite", "unit.tftest.hcl")
	testsupport.WriteFile(t, path, strings.ReplaceAll(readFile(t, path), "./tests/setup", "./suite/setup"))
	before := treeDigest(t, module)

	request := baseConfig(t, module)
	request.TestDirectory = "suite"
	request.NoCache = true

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run with a custom test directory: %v", err)
	}

	if result.Baseline.Runs != 2 {
		t.Fatalf("baseline executed %d run blocks, want the helper and root runs", result.Baseline.Runs)
	}

	assertNoMutantsUnder(t, "suite/", result)
	assertMutationTiersIn(t, "child/main.tf", result)
	assertTreeUnchanged(t, module, before)
}

func TestTheModuleRootStaysMutableUnderARootColocatedTestDirectory(t *testing.T) {
	t.Parallel()

	for _, syntax := range []string{hclTestSyntax, jsonTestSyntax} {
		t.Run(syntax, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, testdirHelperFixture)
			if syntax == jsonTestSyntax {
				testsupport.WriteFile(t, filepath.Join(module, mainFile), "output \"greeting\" {\n  value = \"hello\"\n}\n")
				testsupport.WriteFile(t, filepath.Join(module, "modules.tf.json"),
					`{"module":{"child":{"source":"./child"}}}`)
			}

			before, err := engine.Run(t.Context(), previewRequest(t, module))
			if err != nil {
				t.Fatalf("preview with the default test directory: %v", err)
			}

			assertMutationTiersIn(t, "child/main.tf", before)
			assertMutationTiersIn(t, "leaf/main.tf", before)

			if renameErr := os.Rename(filepath.Join(module, "tests", "unit.tftest.hcl"),
				filepath.Join(module, "unit.tftest.hcl")); renameErr != nil {
				t.Fatalf("moving the suite to the module root: %v", renameErr)
			}

			request := previewRequest(t, module)
			request.TestDirectory = "."

			after, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("preview with a root-colocated test directory: %v", err)
			}

			assertNoMutantsUnder(t, "tests/", after)

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
		})
	}
}

func TestAHelperRootedAtTheTestDirectoryIsNotMutated(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, testdirHelperFixture)
	path := filepath.Join(module, "tests", "unit.tftest.hcl")
	suite := readFile(t, path)
	testsupport.WriteFile(t, path, strings.ReplaceAll(suite, "./tests/setup", "./tests"))
	before := treeDigest(t, module)

	preview, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	assertNoMutantsUnder(t, "tests/", preview)
	assertMutationTiersIn(t, "child/main.tf", preview)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run with a helper rooted at the test directory: %v", err)
	}

	assertNoMutantsUnder(t, "tests/", result)
	assertMutationTiersIn(t, "child/main.tf", result)
	assertTreeUnchanged(t, module, before)
}

func TestAProductionModuleUnderTheTestDirectoryStaysMutable(t *testing.T) {
	t.Parallel()

	for _, syntax := range []string{hclTestSyntax, jsonTestSyntax} {
		t.Run(syntax, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, testdirHelperFixture)
			if syntax == jsonTestSyntax {
				testsupport.WriteFile(t, filepath.Join(module, "shared.tf.json"),
					`{"module":{"shared":{"source":"./tests"}}}`)
			} else {
				appendFile(t, filepath.Join(module, mainFile), "\nmodule \"shared\" {\n  source = \"./tests\"\n}\n")
			}

			result, err := engine.Run(t.Context(), previewRequest(t, module))
			if err != nil {
				t.Fatalf("preview of a production module under the test directory: %v", err)
			}

			assertMutationTiersIn(t, "tests/main.tf", result)
			assertMutationTiersIn(t, "tests/setup/main.tf", result)
		})
	}
}

func assertMutationTiersIn(t *testing.T, file string, result report.Report) {
	t.Helper()

	tiers := map[string]bool{}
	for _, mutant := range result.Mutants {
		if mutant.Range.File == file {
			tiers[mutant.Tier] = true
		}
	}

	if !tiers["smoke"] || !tiers["standard"] {
		t.Errorf("mutation tiers in %s = %v, want smoke and standard", file, tiers)
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
