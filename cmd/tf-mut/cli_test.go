package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// The command line is a thin shell over the engine, so these tests check the
// contract the shell owns: flag parsing, reporter selection and exit codes.

// reporterFlag is the flag these cases exercise most.
const reporterFlag = "--reporter"

const fixtureSource = "../../internal/engine/testdata/skeleton"

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
