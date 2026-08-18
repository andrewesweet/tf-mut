package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/skill"
)

// The command line is a thin shell over the engine, so these tests check the
// contract the shell owns: flag parsing, reporter selection and exit codes.

// reporterFlag is the flag these cases exercise most.
const reporterFlag = "--reporter"

const fixtureSource = "../../internal/engine/testdata/skeleton"

// noCacheFlag keeps the suggest wiring cases hermetic.
const noCacheFlag = "--no-cache"

// dryRunFlag is the suggest flag the wiring cases exercise most.
const dryRunFlag = "--dry-run"

func TestRunReportsPseudoTestedResourcesAndExitsWithFindings(t *testing.T) {
	t.Parallel()

	module := fixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	exitCode := run([]string{runCommand, module}, "dev", &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, stderr.String())
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "PSEUDO-TESTED") {
		t.Fatalf("terminal report has no headline:\n%s", rendered)
	}

	if !strings.Contains(rendered, "SURVIVED") {
		t.Fatalf("terminal report has no survivors:\n%s", rendered)
	}
}

func TestPreviewExitsCleanAndListsMutants(t *testing.T) {
	t.Parallel()

	module := fixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	exitCode := run([]string{previewCommand, module}, "dev", &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, stderr.String())
	}

	if !strings.Contains(stdout.String(), "mutants would be generated") {
		t.Fatalf("preview did not list the population:\n%s", stdout.String())
	}
}

func TestJSONReporterEmitsTheVersionedSchema(t *testing.T) {
	t.Parallel()

	module := fixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	exitCode := run([]string{runCommand, reporterFlag, "json", module}, "dev", &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", exitCode, stderr.String())
	}

	decoded := map[string]any{}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}

	if decoded["schema_version"] == nil {
		t.Fatalf("JSON report carries no schema version: %v", decoded)
	}
}

func TestUnknownReporterIsAnOperationalFailure(t *testing.T) {
	t.Parallel()

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	exitCode := run([]string{runCommand, reporterFlag, "yaml", "."}, "dev", &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}

	if !strings.Contains(stderr.String(), "unknown reporter") {
		t.Fatalf("stderr does not explain the failure: %q", stderr.String())
	}
}

func TestMinScoreGateDecidesTheExitCode(t *testing.T) {
	t.Parallel()

	module := fixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	if code := run([]string{runCommand, "--min-score", "0", module}, "dev", &stdout, &stderr); code != 0 {
		t.Fatalf("exit code with a satisfied gate = %d, want 0\n%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()

	if code := run([]string{runCommand, "--min-score", "100", module}, "dev", &stdout, &stderr); code != 1 {
		t.Fatalf("exit code with an unmet gate = %d, want 1\n%s", code, stderr.String())
	}
}

func TestUnreadableModuleIsAnOperationalFailure(t *testing.T) {
	t.Parallel()

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	exitCode := run([]string{runCommand, filepath.Join(t.TempDir(), "absent")}, "dev", &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
}

// fixture copies the walking-skeleton module so the command runs against a
// disposable tree.
func fixture(t *testing.T) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), "skeleton")
	if err := os.CopyFS(target, os.DirFS(fixtureSource)); err != nil {
		t.Fatalf("copying fixture: %v", err)
	}

	return target
}

func TestConfiguredReportersMergeWithTheFlag(t *testing.T) {
	t.Parallel()

	// The flag chooses standard output; a `reporter` block writes its own file
	// as well. A repository that asked for a SARIF artefact on every run should
	// not lose it because someone passed --reporter json once.
	module := t.TempDir()
	sarifPath := filepath.Join(module, "findings.sarif")

	writeModule(t, module)
	writeFile(t, filepath.Join(module, ".tf-mut.hcl"),
		"reporter \"sarif\" {\n  path = \""+sarifPath+"\"\n}\n")

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run([]string{"run", reporterFlag, "json", module}, "test", &stdout, &stderr)
	if code != report.ExitFindings && code != report.ExitClean {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}

	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("standard output is not the JSON the flag asked for:\n%s", stdout.String())
	}

	written, err := os.ReadFile(sarifPath) //nolint:gosec // a path this test chose.
	if err != nil {
		t.Fatalf("the configured reporter wrote no file: %v", err)
	}

	if !strings.Contains(string(written), `"version": "2.1.0"`) {
		t.Fatalf("the configured reporter did not write SARIF:\n%s", string(written))
	}
}

func TestAnUnknownConfiguredReporterIsRefused(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	writeModule(t, module)
	writeFile(t, filepath.Join(module, ".tf-mut.hcl"),
		"reporter \"telepathy\" {\n  path = \"nowhere\"\n}\n")

	stderr := bytes.Buffer{}
	if code := run([]string{"run", module}, "test", &bytes.Buffer{}, &stderr); code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	if !strings.Contains(stderr.String(), "telepathy") {
		t.Fatalf("the refusal does not name the reporter: %s", stderr.String())
	}
}

// writeModule lays down the smallest module the command can run over.
func writeModule(t *testing.T, dir string) {
	t.Helper()

	writeFile(t, filepath.Join(dir, "main.tf"),
		"resource \"terraform_data\" \"app\" {\n  input = \"kept\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.input\n}\n")

	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o750); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}

	writeFile(t, filepath.Join(dir, "tests", "unit.tftest.hcl"),
		"run \"planned\" {\n  command = plan\n\n  assert {\n"+
			"    condition     = output.app == \"kept\"\n"+
			"    error_message = \"the input must survive\"\n  }\n}\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestTheInstalledSkillReferencesOnlyCommandsAndFlagsTheBinaryHas is the
// self-consistency assertion issue #65 requires: the skill is versioned with
// the binary, so every command and flag it teaches must exist in this build's
// own usage text.
func TestTheInstalledSkillReferencesOnlyCommandsAndFlagsTheBinaryHas(t *testing.T) {
	t.Parallel()

	content := skill.Content()

	for _, flagName := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(content, -1) {
		if !strings.Contains(usage, flagName) {
			t.Errorf("the skill teaches %s, which the usage text does not document", flagName)
		}
	}

	for _, command := range regexp.MustCompile("`tf-mut ([a-z]+)").FindAllStringSubmatch(content, -1) {
		if !strings.Contains(usage, "\n  "+command[1]+" ") {
			t.Errorf("the skill teaches `tf-mut %s`, which is not a command of this binary", command[1])
		}
	}
}

func TestSkillInstallIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stdout := bytes.Buffer{}

	code := run([]string{"skill", "install", "--agent", "generic", "--path", root},
		"test", &stdout, &bytes.Buffer{})
	if code != exitSuccess {
		t.Fatalf("exit code = %d, want success", code)
	}

	if !strings.Contains(stdout.String(), "installed") {
		t.Fatalf("no outcome was reported: %s", stdout.String())
	}

	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "tf-mut-mutation.md")); err != nil {
		t.Fatalf("the generic skill was not placed: %v", err)
	}
}

// TestSuggestIsWiredThroughTheCommandLine is the PR #69 review's critical
// reproduction: `tf-mut suggest` must reach the engine as a suggest run, not
// as a plain run with five dead flags.
func TestSuggestIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	module := suggestFixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run([]string{suggestCommand, dryRunFlag, noCacheFlag, reporterFlag, reporterJSON, module},
		"test", &stdout, &stderr)
	if code != report.ExitClean {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	decoded := decodeReport(t, stdout.Bytes())
	if decoded.Command != report.CommandSuggest {
		t.Fatalf("command = %s, want suggest", decoded.Command)
	}

	if len(decoded.Suggestions) == 0 {
		t.Fatal("a dry-run suggest over a survivor-bearing module produced no suggestions")
	}

	for _, suggestion := range decoded.Suggestions {
		if suggestion.Status != report.SuggestionCandidate {
			t.Fatalf("a dry run produced %s; it must verify nothing", suggestion.Status)
		}
	}
}

func TestSuggestSurvivorSelectionIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	module := suggestFixture(t)
	stderr := bytes.Buffer{}

	// A stale identifier must be an operational failure naming it — which it
	// can only be if --survivor actually reaches the engine.
	code := run([]string{suggestCommand, dryRunFlag, noCacheFlag, "--survivor", "000000000000", module},
		"test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	if !strings.Contains(stderr.String(), "000000000000") {
		t.Fatalf("the failure does not name the stale identifier: %s", stderr.String())
	}
}

func TestSuggestApplySelectionIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	module := suggestFixture(t)

	stdout := bytes.Buffer{}

	// Applying an unknown identifier must be refused by name — which it can
	// only be if --apply actually reaches the engine.
	code := run([]string{suggestCommand, noCacheFlag, reporterFlag, reporterJSON, "--apply", "ffffffffffff", module},
		"test", &stdout, &bytes.Buffer{})
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d on a refused apply", code, report.ExitOperational)
	}

	decoded := decodeReport(t, stdout.Bytes())
	if decoded.Apply == nil || !strings.Contains(decoded.Apply.Aborted, "ffffffffffff") {
		t.Fatalf("the refusal does not name the unknown identifier: %+v", decoded.Apply)
	}
}

// suggestFixture lays down a module with one asserted and one ignored
// resource, so a suggest run has a survivor to work with.
func suggestFixture(t *testing.T) string {
	t.Helper()

	module := t.TempDir()

	writeFile(t, filepath.Join(module, "main.tf"),
		"resource \"terraform_data\" \"asserted\" {\n  input = \"kept\"\n}\n\n"+
			"resource \"terraform_data\" \"ignored\" {\n  input = \"unchecked\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.asserted.input\n}\n\n"+
			"output \"ignored\" {\n  value = terraform_data.ignored.input\n}\n")

	if err := os.MkdirAll(filepath.Join(module, "tests"), 0o750); err != nil {
		t.Fatalf("creating the test directory: %v", err)
	}

	writeFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"),
		"run \"applied\" {\n  command = apply\n\n  assert {\n"+
			"    condition     = output.app == \"kept\"\n"+
			"    error_message = \"the input must survive\"\n  }\n}\n")

	return module
}

func decodeReport(t *testing.T, encoded []byte) report.Report {
	t.Helper()

	decoded := report.Report{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding the JSON report: %v", err)
	}

	return decoded
}

// TestArgumentsAfterTheModulePathAreRefused is the round-3 review's ordering
// finding: Go's flag parsing stops at the first non-flag argument, so
// `tf-mut suggest . --dry-run` silently verified anyway. Trailing arguments
// are now an error naming them.
func TestArgumentsAfterTheModulePathAreRefused(t *testing.T) {
	t.Parallel()

	stderr := bytes.Buffer{}

	code := run([]string{suggestCommand, ".", dryRunFlag, "--survivor", "deadbeef"},
		"test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	for _, expected := range []string{dryRunFlag, "before the module path"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}
