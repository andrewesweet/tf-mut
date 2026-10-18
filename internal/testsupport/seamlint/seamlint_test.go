package seamlint_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
		{file: "import_breach_test.go", rule: seamlint.RuleEngineDrivenImport, count: 2},
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

func TestEngineDrivenImportsRespectOwnership(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"discovery", "mutation", "fingerprint", "oracle", "suggest", "characterise",
		"tfexec", "sandbox", "store", "config", "tracing",
	} {
		for _, owner := range []string{name, "engine", "other"} {
			t.Run(name+"/"+owner, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				directory := filepath.Join(root, "internal", owner)
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				importPath := "github.com/andrewesweet/tf-mut/internal/" + name
				source := fmt.Sprintf("package %s_test\nimport _ %q\n", owner, importPath)
				if err := os.WriteFile(filepath.Join(directory, "driver_test.go"), []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}

				violations, err := seamlint.Scan(root)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if owner == "other" {
					want = 1
				}
				if len(violations) != want {
					t.Fatalf("violations = %v, want %d", violations, want)
				}
				for _, violation := range violations {
					if violation.Rule != seamlint.RuleEngineDrivenImport || violation.Detail != importPath {
						t.Errorf("unexpected violation: %v", violation)
					}
				}
			})
		}
	}
}

func TestProtectedConstructionsResolveCollectionsAndAliases(t *testing.T) {
	t.Parallel()

	for _, protected := range []struct {
		name, imports, typeName string
		rule                    seamlint.Rule
	}{
		{"settings", "", "settings", seamlint.RuleSettingsConstruction},
		{"runner", `import execution "github.com/andrewesweet/tf-mut/internal/tfexec"`, "execution.Runner", seamlint.RuleRunnerConstruction},
		{"dot-runner", `import . "github.com/andrewesweet/tf-mut/internal/tfexec"`, "Runner", seamlint.RuleRunnerConstruction},
	} {
		for _, construction := range []struct {
			name, source string
		}{
			{"literal", "_ = TYPE{}"},
			{"variable", "var value TYPE; _ = value"},
			{"new", "_ = new(TYPE)"},
			{"slice", "_ = []TYPE{{}}"},
			{"array", "_ = [1]TYPE{{}}"},
			{"map-value", `_ = map[string]TYPE{"fixture": {}}`},
			{"map-key", "_ = map[TYPE]string{{}: \"fixture\"}"},
			{"nested", `_ = map[string][]*TYPE{"fixture": {{}}}`},
			{"struct", "_ = []struct { Value TYPE }{{}}"},
			{"slice-variable", "var values []TYPE; _ = values"},
			{"array-variable", "var values [1]TYPE; _ = values"},
			{"map-variable", "var values map[string]TYPE; _ = values"},
			{"new-array", "_ = new([1]TYPE)"},
			{"make-slice", "_ = make([]TYPE, 1)"},
			{"make-map", "_ = make(map[string]TYPE)"},
			{"local-alias", "type local = TYPE; _ = local{}"},
			{"alias-literal", "_ = alias{}"},
			{"alias-variable", "var value alias; _ = value"},
			{"alias-new", "_ = new(alias)"},
			{"alias-slice", "_ = []alias{{}}"},
			{"alias-map", `_ = map[string]alias{"fixture": {}}`},
			{"alias-collection", "_ = collection{{}}"},
			{"parentheses", "_ = new((alias))"},
		} {
			t.Run(protected.name+"/"+construction.name, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				directory := filepath.Join(root, "internal", "engine")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				aliases := fmt.Sprintf("package engine\n%s\ntype direct = %s\ntype alias = direct\ntype collection = []alias\n", protected.imports, protected.typeName)
				if err := os.WriteFile(filepath.Join(directory, "aliases.go"), []byte(aliases), 0o600); err != nil {
					t.Fatal(err)
				}
				body := strings.ReplaceAll(construction.source, "TYPE", protected.typeName)
				source := fmt.Sprintf("package engine\n%s\nfunc construct() { %s }\n", protected.imports, body)
				if err := os.WriteFile(filepath.Join(directory, "construction_test.go"), []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}

				violations, err := seamlint.Scan(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(violations) != 1 || violations[0].Rule != protected.rule {
					t.Fatalf("violations = %v, want one %s violation", violations, protected.rule)
				}
			})
		}
	}
}

func TestAllowedConstructionsRemainClean(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name, directory, source string
	}{
		{"own-runner", "internal/tfexec", "package tfexec\nfunc construct() { type alias = Runner; _ = []alias{{}} }"},
		{"external-own-runner", "internal/tfexec", `package tfexec_test
import execution "github.com/andrewesweet/tf-mut/internal/tfexec"
func construct() { type alias = execution.Runner; _ = []alias{{}} }`},
		{"requests", "internal/engine", "package engine\nfunc construct() { type alias = RunRequest; _ = []alias{{}} }"},
		{"type-positions", "internal/engine", "package engine\nfunc inspect(value settings) {}\ntype holder struct { Value settings }"},
		{"unused-alias", "internal/engine", "package engine\ntype alias = settings"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			directory := filepath.Join(root, testCase.directory)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "clean_test.go"), []byte(testCase.source), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := seamlint.Check(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 0 {
				t.Fatalf("clean tree has violations: %v", violations)
			}
		})
	}
}
