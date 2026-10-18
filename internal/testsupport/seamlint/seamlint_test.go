package seamlint_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/testsupport/seamlint"
)

// callerDir resolves the directory holding this test file.
func callerDir(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolving this file's path")
	}

	return filepath.Dir(thisFile)
}

// repoRoot resolves the module root: the seam-lint package sits three
// directories below it.
func repoRoot(t *testing.T) string {
	t.Helper()

	return filepath.Clean(filepath.Join(callerDir(t), "..", "..", ".."))
}

// fixturesDir resolves the breaching and clean fixtures' directory. The scan
// root is the directory itself: the scanner skips testdata below a root, not
// the root it is handed.
func fixturesDir(t *testing.T) string {
	t.Helper()

	return filepath.Join(callerDir(t), "testdata")
}

// TestThatTheCurrentTreeHoldsTheSeam is the standing gate: every test file
// in the module obeys the mechanical seam rules once the reviewed exceptions
// are applied. A new breach lands here first, before any reviewer sees it.
func TestThatTheCurrentTreeHoldsTheSeam(t *testing.T) {
	t.Parallel()

	violations, err := seamlint.Check(repoRoot(t))
	if err != nil {
		t.Fatalf("scanning the repository: %v", err)
	}

	if len(violations) != 0 {
		for _, violation := range violations {
			t.Errorf("%s: %s: %s", violation.Position, violation.Rule, violation.Detail)
		}

		t.Fatalf("the tree carries %d seam violation(s) beyond the recorded exceptions", len(violations))
	}
}

// TestThatTheSeamScanAndTheRecordedExceptionsCoverEachOther keeps the
// exception list honest in both directions: every recorded exception is
// still live — the scanner still flags its file for its rule, so the list
// cannot quietly outlive the code it excuses — and every violation the
// scanner finds is covered by an exception, so nothing unreviewed slips
// through.
func TestThatTheSeamScanAndTheRecordedExceptionsCoverEachOther(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	violations, err := seamlint.Scan(root)
	if err != nil {
		t.Fatalf("scanning the repository: %v", err)
	}

	exceptions := seamlint.Exceptions()
	if len(exceptions) == 0 {
		t.Fatal("the exception list is empty; a list that has never been exercised cannot stay honest")
	}

	for _, exception := range exceptions {
		if exception.Reason == "" {
			t.Errorf("exception %s carries no reason", exception.Path)
		}

		if _, err := os.Stat(filepath.Join(root, exception.Path)); err != nil {
			t.Errorf("exception %s names no file in the tree: %v", exception.Path, err)
		}

		live := slices.ContainsFunc(violations, func(violation seamlint.Violation) bool {
			return violation.Position.Filename == exception.Path && violation.Rule == exception.Rule
		})
		if !live {
			t.Errorf("exception %s (%s) is stale: the scanner no longer flags it", exception.Path, exception.Rule)
		}
	}

	for _, violation := range violations {
		recorded := slices.ContainsFunc(exceptions, func(exception seamlint.Exception) bool {
			return exception.Path == violation.Position.Filename && exception.Rule == violation.Rule
		})
		if !recorded {
			t.Errorf("%s: %s: %s is a breach no exception records", violation.Position, violation.Rule, violation.Detail)
		}
	}
}

// TestThatTheSeamRulesFlagTheirFixtureBreaches proves the scanner catches
// each rule against a deliberately breaching fixture, and counts every form
// the rule covers: the composite literal, the declared variable and the new
// call.
func TestThatTheSeamRulesFlagTheirFixtureBreaches(t *testing.T) {
	t.Parallel()

	violations, err := seamlint.Scan(fixturesDir(t))
	if err != nil {
		t.Fatalf("scanning the fixtures: %v", err)
	}

	for _, breach := range []struct {
		file  string
		rule  seamlint.Rule
		count int
	}{
		{file: "settings_breach_test.go", rule: seamlint.RuleSettingsConstruction, count: 3},
		{file: "runner_breach_test.go", rule: seamlint.RuleRunnerConstruction, count: 3},
		{file: "import_breach_test.go", rule: seamlint.RuleEngineDrivenImport, count: 1},
	} {
		t.Run(breach.file, func(t *testing.T) {
			t.Parallel()

			count := 0
			for _, violation := range violations {
				if violation.Position.Filename == breach.file && violation.Rule == breach.rule {
					count++
				}
			}

			if count != breach.count {
				t.Fatalf("the scanner reported %d %s violation(s) in %s, want %d", count, breach.rule, breach.file, breach.count)
			}
		})
	}
}

// TestThatTheSeamScanLeavesTheCleanFixtureAlone proves the scanner reads the
// rule and not the shape of engine use: a test that builds a request and
// asserts the report — the seam's own idiom — raises nothing.
func TestThatTheSeamScanLeavesTheCleanFixtureAlone(t *testing.T) {
	t.Parallel()

	violations, err := seamlint.Scan(fixturesDir(t))
	if err != nil {
		t.Fatalf("scanning the fixtures: %v", err)
	}

	for _, violation := range violations {
		if violation.Position.Filename == "clean_test.go" {
			t.Errorf("%s: %s: the clean fixture drives the seam as the rule prescribes", violation.Position, violation.Rule)
		}
	}
}
