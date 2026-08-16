package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command line is a thin shell over the engine, so these tests check the
// contract the shell owns: flag parsing, reporter selection and exit codes.

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

	exitCode := run([]string{runCommand, "--reporter", "json", module}, "dev", &stdout, &stderr)
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

	exitCode := run([]string{runCommand, "--reporter", "yaml", "."}, "dev", &stdout, &stderr)
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
