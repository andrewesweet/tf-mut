package engine_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators
// is the M5a headline proof: the operator-matrix fixture's standard report from
// a binary built at the merge-base with the default branch and one built from
// this tree are identical under matched cache-off legs, with
// baseline.duration_ms excluded and nothing else excluded.
//
// One normalisation applies to both legs: each mutant's diagnostics are put
// into a canonical order before the comparison. Terraform's parallel
// evaluation emits one plan's errors in a nondeterministic order — measured
// run-to-run for one and the same binary — while the set is stable, and the
// M2 honesty gate already holds states and diagnoses independent of order.
// Since M5a the engine itself publishes the canonical order; the base leg's
// binary predates that, so the proof normalises rather than compares the
// unobservable stream order on either side. Every field's CONTENT is compared.
//
// The legs run the CLI, not the seam, because the claim is about the published
// document two different binaries emit.
func TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	base := defaultBranchMergeBase(t, root)
	if base == "" {
		t.Skip("no default-branch ref found; the before-and-after legs need one")
	}

	if base == headCommit(t, root) && workingTreeIsClean(t, root) {
		t.Skip("this tree is the merge-base; the legs are one commit and run on the pull request")
	}

	baseTree := t.TempDir()
	archiveCommit(t, root, base, baseTree)

	// One module path for both legs: the report carries the module's absolute
	// path, so the legs must differ by the binary alone.
	module := filepath.Join(t.TempDir(), "module")

	copyFixtureInto(t, "operators", module)
	before := standardReport(t,
		buildBinary(t, baseTree, filepath.Join(t.TempDir(), "tf-mut-base")), module)

	if err := os.RemoveAll(module); err != nil {
		t.Fatalf("resetting the module between legs: %v", err)
	}

	copyFixtureInto(t, "operators", module)
	afterBinary := buildBinary(t, root, filepath.Join(t.TempDir(), "tf-mut-head"))
	after := standardReport(t, afterBinary, module)

	assertDiagnosticsAreCanonicallyOrdered(t, after)

	for _, document := range []map[string]any{before, after} {
		baseline, ok := document["baseline"].(map[string]any)
		if !ok {
			t.Fatal("the report carries no baseline object")
		}

		delete(baseline, "duration_ms")
		canonicaliseDiagnosticOrder(document)
	}

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the standard report of the matrix fixture moved under M5a:\n  %s",
			firstDifference(before, after, "$"))
	}
}

// assertDiagnosticsAreCanonicallyOrdered holds the engine's published-order
// guarantee: since M5a a report's diagnostics are non-decreasing under the
// canonical comparison, so consumers can diff reports run against run.
func assertDiagnosticsAreCanonicallyOrdered(t *testing.T, document map[string]any) {
	t.Helper()

	mutants, ok := document["mutants"].([]any)
	if !ok {
		t.Fatal("the report carries no mutants list")
	}

	for index, entry := range mutants {
		mutant, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("mutant %d is not an object", index)
		}

		diagnostics, present := mutant["diagnostics"].([]any)
		if !present {
			// Absent and null both mean the mutant carries none; neither is
			// an ordering claim.
			continue
		}

		keys := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			object, ok := diagnostic.(map[string]any)
			if !ok {
				t.Fatalf("mutant %d carries a non-object diagnostic", index)
			}

			keys = append(keys, diagnosticSortKey(object))
		}

		if !slices.IsSorted(keys) {
			t.Fatalf("mutant %d (%v at %v) publishes diagnostics out of canonical order: %v",
				index, mutant["operator"], mutant["site"], keys)
		}
	}
}

// canonicaliseDiagnosticOrder sorts every mutant's diagnostics in place, so a
// before leg from a binary that predates the canonical publication order is
// compared on content, not on Terraform's stream order.
func canonicaliseDiagnosticOrder(document map[string]any) {
	mutants, ok := document["mutants"].([]any)
	if !ok {
		return
	}

	for _, entry := range mutants {
		mutant, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		diagnostics, ok := mutant["diagnostics"].([]any)
		if !ok {
			continue
		}

		slices.SortFunc(diagnostics, func(left, right any) int {
			leftObject, leftOK := left.(map[string]any)
			rightObject, rightOK := right.(map[string]any)
			if !leftOK || !rightOK {
				return 0
			}

			return strings.Compare(diagnosticSortKey(leftObject), diagnosticSortKey(rightObject))
		})
	}
}

// diagnosticSortKey renders one diagnostic as the canonical comparison string
// the engine orders by: the run it came from, then the message, then the
// location line by line, column by column — the same tie-break order as the
// engine's comparator, spelled structurally because the decoded range is a
// map whose default print orders columns before lines.
func diagnosticSortKey(diagnostic map[string]any) string {
	rangeKey := ""
	if diagnosticRange, ok := diagnostic["range"].(map[string]any); ok {
		rangeKey = fmt.Sprintf("%v/%v-%v",
			diagnosticRange["file"],
			positionKey(diagnosticRange["start"]),
			positionKey(diagnosticRange["end"]))
	}

	return fmt.Sprintf("%v|%v|%v|%v|%v|%s",
		diagnostic["test_file"], diagnostic["test_run"],
		diagnostic["severity"], diagnostic["summary"], diagnostic["detail"],
		rangeKey)
}

func positionKey(position any) string {
	object, ok := position.(map[string]any)
	if !ok {
		return fmt.Sprintf("%v", position)
	}

	// Zero-padded so the string order is the numeric order the engine
	// compares by; line 59 sorts before line 127 only when the digits
	// align.
	return fmt.Sprintf("%06d:%06d", number(object["line"]), number(object["column"]))
}

func number(value any) int {
	asFloat, ok := value.(float64)
	if !ok {
		return 0
	}

	return int(asFloat)
}

// defaultBranchMergeBase merges HEAD with the default branch, trying the
// spellings a checkout is likely to carry.
func defaultBranchMergeBase(t *testing.T, root string) string {
	t.Helper()

	for _, ref := range []string{"origin/master", "origin/main", "master", "main"} {
		if !refExists(t, root, ref) {
			continue
		}

		return strings.TrimSpace(gitStdout(t, root, "merge-base", "HEAD", ref))
	}

	return ""
}

func refExists(t *testing.T, root, ref string) bool {
	t.Helper()

	//nolint:gosec // literal arguments, repository-owned path.
	command := exec.CommandContext(t.Context(), "git", "-C", root,
		"rev-parse", "--verify", "--quiet", ref+"^{commit}")
	command.Env = subprocessEnvironment()

	return command.Run() == nil
}

func headCommit(t *testing.T, root string) string {
	t.Helper()

	return strings.TrimSpace(gitStdout(t, root, "rev-parse", "HEAD"))
}

func workingTreeIsClean(t *testing.T, root string) bool {
	t.Helper()

	return gitStdout(t, root, "status", "--porcelain") == ""
}

// archiveCommit materialises one commit's tree, without its history, so the
// base binary builds from exactly the before-M5a sources.
func archiveCommit(t *testing.T, root, commit, target string) {
	t.Helper()

	archive := filepath.Join(t.TempDir(), "base.tar")
	gitStdout(t, root, "archive", "--format=tar", "-o", archive, commit)

	command := exec.CommandContext(t.Context(), "tar", "-xf", archive, "-C", target) //nolint:gosec // test-owned paths.
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("extracting %s: %v\n%s", archive, err, output)
	}
}

// buildBinary builds the CLI from a source tree into an output path. The child
// inherits the toolchain environment the test itself runs under; nothing is
// written into the source tree.
func buildBinary(t *testing.T, sourceTree, output string) string {
	t.Helper()

	//nolint:gosec // fixed binary name, repository-owned path.
	command := exec.CommandContext(t.Context(), "go", "build",
		"-mod=readonly", "-o", output, "./cmd/tf-mut")
	command.Dir = sourceTree
	command.Env = subprocessEnvironment()

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI from %s: %v\n%s", sourceTree, err, output)
	}

	return output
}

// standardReport runs one cache-off standard leg and decodes the JSON report.
// The exit code is the finding signal, not a failure: a report is a report.
func standardReport(t *testing.T, binary, module string) map[string]any {
	t.Helper()

	//nolint:gosec // test-built binary, literal arguments.
	command := exec.CommandContext(t.Context(), binary,
		"run", "--tier", "standard", "--no-cache", "--reporter", "json", module)
	command.Dir = module
	command.Env = subprocessEnvironment()

	output, err := command.Output()
	if err != nil && !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("running the standard leg: %v\n%s", err, output)
	}

	document := map[string]any{}
	if err := json.Unmarshal(output, &document); err != nil {
		t.Fatalf("decoding the standard leg's report: %v", err)
	}

	return document
}

// subprocessEnvironment hands a child process the ambient environment minus
// everything that would redirect it at another location — the repository's
// standing rule for anything that runs a subprocess outside this tree.
func subprocessEnvironment() []string {
	redirecting := []string{
		"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
		"GIT_COMMON_DIR=", "BASH_ENV=", "ENV=", "CDPATH=",
	}

	environment := []string{"CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1"}

	for _, entry := range os.Environ() {
		blocked := false

		for _, prefix := range redirecting {
			if strings.HasPrefix(entry, prefix) {
				blocked = true

				break
			}
		}

		if !blocked {
			environment = append(environment, entry)
		}
	}

	return environment
}

// copyFixtureInto copies a fixture to a chosen path, for the invariance legs
// that must run both binaries over one absolute module path.
func copyFixtureInto(t *testing.T, name, target string) {
	t.Helper()

	if err := os.CopyFS(target, os.DirFS(filepath.Join(fixtureRoot, name))); err != nil {
		t.Fatalf("copying fixture %s to %s: %v", name, target, err)
	}
}

// firstDifference walks two decoded reports and describes the first path that
// differs, so a moved report names its mover instead of failing opaquely.
func firstDifference(before, after any, path string) string {
	if beforeMap, beforeIsMap := before.(map[string]any); beforeIsMap {
		if afterMap, afterIsMap := after.(map[string]any); afterIsMap {
			return firstDifferentKey(beforeMap, afterMap, path)
		}
	}

	if beforeList, beforeIsList := before.([]any); beforeIsList {
		if afterList, afterIsList := after.([]any); afterIsList {
			return firstDifferentIndex(beforeList, afterList, path)
		}
	}

	return ""
}

func firstDifferentKey(before, after map[string]any, path string) string {
	for _, key := range sortedKeys(before) {
		beforeValue := before[key]
		afterValue, inAfter := after[key]

		switch {
		case !inAfter:
			return fmt.Sprintf("%s.%s is absent after M5a (was %v)", path, key, beforeValue)
		case !reflect.DeepEqual(beforeValue, afterValue):
			if nested := firstDifference(beforeValue, afterValue, path+"."+key); nested != "" {
				return nested
			}

			return fmt.Sprintf("%s.%s: %v != %v", path, key, beforeValue, afterValue)
		}
	}

	for _, key := range sortedKeys(after) {
		if _, present := before[key]; !present {
			return fmt.Sprintf("%s.%s is new after M5a: %v", path, key, after[key])
		}
	}

	return ""
}

func firstDifferentIndex(before, after []any, path string) string {
	for index := range min(len(before), len(after)) {
		if reflect.DeepEqual(before[index], after[index]) {
			continue
		}

		at := fmt.Sprintf("%s[%d]", path, index)
		if nested := firstDifference(before[index], after[index], at); nested != "" {
			return nested
		}

		return fmt.Sprintf("%s: %v != %v", at, before[index], after[index])
	}

	if len(before) != len(after) {
		return fmt.Sprintf("%s: %d entries before, %d after", path, len(before), len(after))
	}

	return ""
}

func sortedKeys(document map[string]any) []string {
	keys := make([]string, 0, len(document))
	for key := range document {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
