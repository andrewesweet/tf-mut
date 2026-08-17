package engine_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3b.1 (#45): `--since` selects over the union of the committed range, the
// working tree, and untracked Terraform, test and configuration files (spec
// review C5). Unresolvable situations error — never a silent full or empty
// run. Sampling is deterministic and non-authoritative. Every case here drives
// the engine seam; the git fixtures are built per test in a temporary clone.

// gitFixture copies a fixture into a fresh git repository and commits it. The
// git helper and its stripped environment come from remote_test.go.
func gitFixture(t *testing.T, name string) string {
	t.Helper()

	module := copyFixture(t, name)
	git(t, module, "init", "--quiet", "--initial-branch=main")
	git(t, module, "add", "--all")
	commit(t, module, "initial")

	return module
}

// gitStdout runs one git command and returns its output, for the commands
// whose result the test needs rather than just their effect.
func gitStdout(t *testing.T, dir string, args ...string) string {
	t.Helper()

	//nolint:gosec // every argument is a literal or a test-owned temporary path.
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	command.Env = gitEnvironment()

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}

	return string(output)
}

func commit(t *testing.T, dir, message string) {
	t.Helper()

	//nolint:gosec // fixed binary, test-owned arguments.
	command := exec.CommandContext(t.Context(), "git",
		"-C", dir, "commit", "--quiet", "--no-verify", "--no-gpg-sign", "-m", message)
	command.Env = append(gitEnvironment(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@invalid")

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
}

// sincePreview previews the module with --since semantics.
func sincePreview(t *testing.T, module, ref string) report.Report {
	t.Helper()

	config := baseConfig(t, module)
	config.Preview = true
	config.Since = ref

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview --since %s: %v", ref, err)
	}

	return result
}

func mutantFiles(result report.Report) []string {
	files := []string{}

	for _, mutant := range result.Mutants {
		if !slices.Contains(files, mutant.Range.File) {
			files = append(files, mutant.Range.File)
		}
	}

	slices.Sort(files)

	return files
}

const extraResource = `resource "terraform_data" "extra" {
  input = "fresh"
}
`

// sinceHead scopes to the working tree alone.
const sinceHead = "HEAD"

// TestAnUncommittedNewResourceIsSelected is the demo case: the inner loop
// tests what the author is actually editing, before any commit exists.
func TestAnUncommittedNewResourceIsSelected(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)
	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)

	result := sincePreview(t, module, sinceHead)

	files := mutantFiles(result)
	if !slices.Equal(files, []string{"extra.tf"}) {
		t.Fatalf("expected only extra.tf mutants, got %v", files)
	}

	if result.Population.Selected == 0 || result.Population.Omitted == 0 {
		t.Fatalf("selection must report both populations distinctly: %+v", result.Population)
	}

	for _, mutant := range result.Mutants {
		if mutant.Provenance == nil || mutant.Provenance.Selection != report.SelectionSince {
			t.Fatalf("mutant %s carries no since provenance: %+v", mutant.ID, mutant.Provenance)
		}
	}
}

// TestStagedAndUnstagedChangesAreSelected covers the other two arms of the
// working-tree union.
func TestStagedAndUnstagedChangesAreSelected(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)

	// Staged: a new file added to the index.
	writeFile(t, filepath.Join(module, "staged.tf"), extraResource)
	git(t, module, "add", "staged.tf")

	// Unstaged: an edit to a committed file.
	appendFile(t, filepath.Join(module, "child", "main.tf"), "\noutput \"more\" {\n  value = \"x\"\n}\n")

	files := mutantFiles(sincePreview(t, module, sinceHead))

	expected := []string{"child/main.tf", "staged.tf"}
	if !slices.Equal(files, expected) {
		t.Fatalf("expected %v, got %v", expected, files)
	}
}

// TestACommittedRangeIsSelected covers the committed arm: changes between the
// ref and HEAD select their files.
func TestACommittedRangeIsSelected(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)
	git(t, module, "tag", "before")

	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)
	git(t, module, "add", "--all")
	commit(t, module, "add extra")

	files := mutantFiles(sincePreview(t, module, "before"))
	if !slices.Equal(files, []string{"extra.tf"}) {
		t.Fatalf("expected only extra.tf mutants, got %v", files)
	}
}

// TestARenameFollowsBothNames: mutants in the renamed file are selected even
// though its content never changed.
func TestARenameFollowsBothNames(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)
	git(t, module, "tag", "before")
	git(t, module, "mv", "main.tf", "renamed.tf")
	commit(t, module, "rename")

	files := mutantFiles(sincePreview(t, module, "before"))
	if !slices.Contains(files, "renamed.tf") {
		t.Fatalf("the renamed file's mutants were not selected: %v", files)
	}
}

// TestADeletionSelectsTheWholeModule: the deleted content cannot be resolved,
// so the module that contained it is selected conservatively.
func TestADeletionSelectsTheWholeModule(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)

	// Give the root module a second file, then delete it.
	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)
	git(t, module, "add", "--all")
	commit(t, module, "add extra")
	git(t, module, "tag", "before")
	git(t, module, "rm", "--quiet", "extra.tf")
	commit(t, module, "delete extra")

	files := mutantFiles(sincePreview(t, module, "before"))

	// The whole root module — but not the untouched child.
	if !slices.Contains(files, "main.tf") {
		t.Fatalf("the deleted file's module was not selected: %v", files)
	}

	if slices.Contains(files, "child/main.tf") {
		t.Fatalf("an untouched module was selected by a deletion elsewhere: %v", files)
	}
}

// TestAMissingRefIsAnError: a wrong ref can never fake a green gate.
func TestAMissingRefIsAnError(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)

	config := baseConfig(t, module)
	config.Preview = true
	config.Since = "no-such-ref"

	if _, err := engine.Run(t.Context(), config); err == nil {
		t.Fatal("an unknown ref produced a run instead of an error")
	}
}

// TestOutsideARepositoryIsAnError: no repository means no diff to scope by.
func TestOutsideARepositoryIsAnError(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, discriminateFixture)

	config := baseConfig(t, module)
	config.Preview = true
	config.Since = sinceHead

	if _, err := engine.Run(t.Context(), config); err == nil {
		t.Fatal("a module outside any repository produced a scoped run instead of an error")
	}
}

// TestAMergeConflictIsAnError: an in-progress conflict makes the working tree
// undiffable, and the answer is an error rather than a guess.
func TestAMergeConflictIsAnError(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)

	// Stage a conflicted index entry directly: three stages for one path.
	blob := strings.TrimSpace(gitStdout(t, module, "hash-object", "-w", "main.tf"))
	info := strings.Builder{}

	for stage := 1; stage <= 3; stage++ {
		fmt.Fprintf(&info, "100644 %s %d\tmain.tf\n", blob, stage)
	}

	applyIndexInfo(t, module, info.String())

	config := baseConfig(t, module)
	config.Preview = true
	config.Since = sinceHead

	if _, err := engine.Run(t.Context(), config); err == nil {
		t.Fatal("a conflicted index produced a scoped run instead of an error")
	}
}

// TestAShallowCloneLackingTheRefIsAnError: the ref exists in the full history
// but not in the shallow clone, and the answer is an error.
func TestAShallowCloneLackingTheRefIsAnError(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)
	git(t, module, "tag", "before")
	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)
	git(t, module, "add", "--all")
	commit(t, module, "add extra")

	shallow := filepath.Join(t.TempDir(), "shallow")
	git(t, module, "clone", "--quiet", "--depth", "1", "file://"+module, shallow)

	config := baseConfig(t, shallow)
	config.Preview = true
	config.Since = "before"

	if _, err := engine.Run(t.Context(), config); err == nil {
		t.Fatal("a shallow clone lacking the ref produced a scoped run instead of an error")
	}
}

// TestNonTerraformClassChangesForceTheFullPopulation: one case per file class
// in #33's exact list — a change to any non-.tf class cannot be scoped.
func TestNonTerraformClassChangesForceTheFullPopulation(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"tests/extra.tftest.hcl": "run \"noop\" {\n  command = plan\n}\n",
		"extra.tfvars":           "x = 1\n",
		"auto.auto.tfvars":       "y = 2\n",
		".tf-mut.hcl":            "",
		".terraform.lock.hcl":    "# lock\n",
	}

	for path, content := range cases {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			module := gitFixture(t, discriminateFixture)
			full := preview(t, module, nil)

			writeFile(t, filepath.Join(module, path), content)

			result := sincePreview(t, module, sinceHead)
			if len(result.Mutants) != len(full.Mutants) {
				t.Fatalf("a %s change selected %d of %d mutants; it must force the full population",
					path, len(result.Mutants), len(full.Mutants))
			}
		})
	}
}

// TestSinceSelectionIsDeterministic: the same command twice selects the same
// population.
func TestSinceSelectionIsDeterministic(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, discriminateFixture)
	writeFile(t, filepath.Join(module, "extra.tf"), extraResource)

	first := sincePreview(t, module, sinceHead)
	second := sincePreview(t, module, sinceHead)

	if !slices.Equal(mutantIDs(first), mutantIDs(second)) {
		t.Fatal("two identical --since previews selected different populations")
	}
}

// TestSamplingIsDeterministicAndNonAuthoritative: same seed, same subset;
// different seed, labelled; the report carries the sampling metadata.
func TestSamplingIsDeterministicAndNonAuthoritative(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "operators")

	sample := func(seed int64) report.Report {
		config := baseConfig(t, module)
		config.Preview = true
		config.SamplePercent = 30
		config.HasSample = true
		config.SampleSeed = seed

		result, err := engine.Run(t.Context(), config)
		if err != nil {
			t.Fatalf("sampled preview: %v", err)
		}

		return result
	}

	first, second := sample(42), sample(42)

	if !slices.Equal(mutantIDs(first), mutantIDs(second)) {
		t.Fatal("the same seed selected different samples")
	}

	if first.Sampling == nil || first.Sampling.Authoritative {
		t.Fatalf("a sampled report must be labelled non-authoritative: %+v", first.Sampling)
	}

	if first.Sampling.Seed != 42 || first.Sampling.RatePercent != 30 {
		t.Fatalf("sampling metadata wrong: %+v", first.Sampling)
	}

	full := preview(t, module, nil)
	if len(first.Mutants) == 0 || len(first.Mutants) >= len(full.Mutants) {
		t.Fatalf("a 30%% sample selected %d of %d mutants", len(first.Mutants), len(full.Mutants))
	}
}

// TestASampledGateIsRefusedWithoutTheOptIn: a lucky sample can never pass CI
// (gate truth table, sampled row).
func TestASampledGateIsRefusedWithoutTheOptIn(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	config := baseConfig(t, module)
	config.HasSample = true
	config.SamplePercent = 50
	config.MinScore = 10
	config.HasMinScore = true

	if _, err := engine.Run(t.Context(), config); err == nil {
		t.Fatal("--sample satisfied --min-score without --allow-sampled-gate")
	}

	config.AllowSampledGate = true

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("--allow-sampled-gate did not permit the sampled gate: %v", err)
	}
}

// TestVerdictInvarianceUnderSince is the levers' law (#33): for any mutant
// executed in both a full and a scoped run, state, diagnosis and evidence are
// identical. The helper is the harness every later lever reuses.
func TestVerdictInvarianceUnderSince(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "all-killed")

	full, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("full run: %v", err)
	}

	config := baseConfig(t, module)
	config.Since = sinceHead

	// Touch the module file so the scoped run selects its mutants.
	appendFile(t, filepath.Join(module, "main.tf"), "\n# touched\n")

	scoped, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("scoped run: %v", err)
	}

	assertVerdictInvariance(t, full, scoped)
}

func mutantIDs(result report.Report) []string {
	ids := make([]string, 0, len(result.Mutants))
	for _, mutant := range result.Mutants {
		ids = append(ids, mutant.ID)
	}

	slices.Sort(ids)

	return ids
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()

	existing, err := os.ReadFile(path) //nolint:gosec // fixture copy under t.TempDir.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	writeFile(t, path, string(existing)+content)
}

func applyIndexInfo(t *testing.T, dir, info string) {
	t.Helper()

	//nolint:gosec // fixed binary, test-owned arguments.
	command := exec.CommandContext(t.Context(), "git", "-C", dir, "update-index", "--index-info")
	command.Stdin = strings.NewReader(info)
	command.Env = gitEnvironment()

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git update-index: %v\n%s", err, output)
	}
}
