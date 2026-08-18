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

	content := skill.Content(skill.NameMutation) + skill.Content(skill.NameCharacterise)

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

	for _, expected := range []string{dryRunFlag, beforeThePath} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}

// TestCharacteriseIsWiredThroughTheCommandLine proves the whole surface
// reaches the engine: the command, the granularity flag and the JSON reporter
// that owns standard output and therefore carries the generated content.
func TestCharacteriseIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	writeFile(t, filepath.Join(module, "main.tf"),
		"resource \"terraform_data\" \"app\" {\n  input = \"kept\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.output\n}\n")

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run([]string{characteriseCommand, reporterFlag, reporterJSON, module},
		"test", &stdout, &stderr)
	if code != report.ExitClean {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	decoded := decodeReport(t, stdout.Bytes())
	if decoded.Command != report.CommandCharacterise {
		t.Fatalf("command = %s, want characterise", decoded.Command)
	}

	if decoded.Characterisation == nil || len(decoded.Characterisation.Files) == 0 {
		t.Fatal("the JSON report carries no generated content")
	}

	if !strings.Contains(decoded.Characterisation.Files[0].Content, "run \"characterise_defaults\"") {
		t.Fatalf("the generated content is not a run block:\n%s",
			decoded.Characterisation.Files[0].Content)
	}
}

// TestTheGranularityFlagReachesTheEngine is the other half of the wiring: a
// flag the shell parsed and dropped would be invisible without a case that
// asserts a value only the engine can produce.
func TestTheGranularityFlagReachesTheEngine(t *testing.T) {
	t.Parallel()

	module := t.TempDir()
	writeFile(t, filepath.Join(module, "main.tf"),
		"resource \"terraform_data\" \"app\" {\n  input = \"kept\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.output\n}\n")

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run([]string{characteriseCommand, "--pin", "configured", reporterFlag, reporterJSON, module},
		"test", &stdout, &stderr)
	if code != report.ExitClean {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	decoded := decodeReport(t, stdout.Bytes())
	if decoded.Characterisation.Rung != "configured" {
		t.Fatalf("rung = %s, want configured", decoded.Characterisation.Rung)
	}
}

// TestCharacteriseRefusesArgumentsAfterTheModulePath extends the round-3
// ordering repair to the new commands by name, rather than by assuming the
// shared parser still covers them.
func TestCharacteriseRefusesArgumentsAfterTheModulePath(t *testing.T) {
	t.Parallel()

	stderr := bytes.Buffer{}

	code := run([]string{characteriseCommand, ".", "--write"}, "test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	for _, expected := range []string{"--write", beforeThePath} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}

// TestTheTodoSurfacesAreWiredInBothArgumentOrders is the M4.5 spec's ninth
// story made falsifiable: a wiring gap in a drain-todos loop is invisible
// until an agent hits it, so each surface is asserted from the command line
// with the module path both before and after the flags.
// beforeThePath is the refusal every command gives for a flag after the path.
const beforeThePath = "before the module path"

func TestTheTodoSurfacesAreWiredInBothArgumentOrders(t *testing.T) {
	t.Parallel()

	for name, order := range map[string]bool{"flags first": false, "path first": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := todoFixture(t)

			listed := decodeReport(t, mustRun(t, arguments(order, module,
				todosCommand, reporterFlag, reporterJSON)))
			if listed.Command != report.CommandTodos {
				t.Fatalf("command = %s, want todos", listed.Command)
			}

			if listed.Characterisation.OpenTodos() != 1 {
				t.Fatalf("listed %d open judgement points, want one",
					listed.Characterisation.OpenTodos())
			}

			identifier := listed.Characterisation.Todos[0].ID

			answered := decodeReport(t, mustRun(t, arguments(order, module,
				characteriseCommand, "--answer", identifier+`="10.0.0.0/16"`,
				reporterFlag, reporterJSON)))
			if answered.Characterisation.OpenTodos() != 0 {
				t.Fatalf("--answer left the judgement point open: %+v",
					answered.Characterisation.Todos)
			}

			if !answered.Characterisation.Complete {
				t.Fatal("the answered characterisation is incomplete")
			}

			resumed := decodeReport(t, mustRun(t, arguments(order, module,
				characteriseCommand, "--resume", reporterFlag, reporterJSON)))
			if resumed.Characterisation.OpenTodos() != 1 {
				t.Fatal("--resume answered a judgement point nobody answered")
			}
		})
	}
}

// arguments places the module path before or after the flags. Only one order
// is legal — Go's flag parsing stops at the first non-flag argument — so the
// path-first order asserts the refusal rather than the result.
func arguments(pathFirst bool, module, command string, flags ...string) []string {
	if pathFirst {
		return append([]string{command, module}, flags...)
	}

	return append(append([]string{command}, flags...), module)
}

// mustRun executes the command line and fails on anything but a clean or
// findings exit, returning standard output.
func mustRun(t *testing.T, args []string) []byte {
	t.Helper()

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run(args, "test", &stdout, &stderr)

	// The path-first order is refused by name, which is the shipped contract:
	// the assertion is that the refusal happens, not that the run succeeds.
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		if code != report.ExitOperational ||
			!strings.Contains(stderr.String(), beforeThePath) {
			t.Fatalf("arguments after the module path were not refused: %d %s",
				code, stderr.String())
		}

		t.SkipNow()
	}

	if code != report.ExitClean && code != report.ExitFindings {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	return stdout.Bytes()
}

// todoFixture is a module whose one input carries a constraint no deterministic
// pipeline can satisfy.
func todoFixture(t *testing.T) string {
	t.Helper()

	module := t.TempDir()
	writeFile(t, filepath.Join(module, "main.tf"),
		"variable \"vpc_cidr\" {\n  type = string\n\n  validation {\n"+
			"    condition     = can(cidrnetmask(var.vpc_cidr))\n"+
			"    error_message = \"vpc_cidr must be a CIDR block\"\n  }\n}\n\n"+
			"resource \"terraform_data\" \"network\" {\n  input = var.vpc_cidr\n}\n\n"+
			"output \"network\" {\n  value = terraform_data.network.output\n}\n")

	return module
}

// TestTodosRefusesArgumentsAfterTheModulePath keeps the round-3 ordering
// repair asserted by name for the third new command.
func TestTodosRefusesArgumentsAfterTheModulePath(t *testing.T) {
	t.Parallel()

	stderr := bytes.Buffer{}

	code := run([]string{todosCommand, ".", "--resume"}, "test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	for _, expected := range []string{"--resume", beforeThePath} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}

// transcriptFence marks the fenced blocks the end-of-MVP gate executes.
const transcriptFence = "```tf-mut-transcript"

// TestTheInstalledSkillsWalkthroughExecutes is the end-of-MVP gate, made
// falsifiable (M4.5 spec review, M9).
//
// The commands are extracted from the *installed* file rather than from a
// script kept beside it, and executed in order against a fixture module. A
// hidden duplicate would prove the binary; reading the installed instructions
// is the only way to prove the instructions.
func TestTheInstalledSkillsWalkthroughExecutes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := skill.Install(root, skill.AgentGeneric, "test", false); err != nil {
		t.Fatalf("skill install: %v", err)
	}

	module := walkthroughFixture(t)

	// The two loops in the order the MVP claim makes: the characterisation
	// loop produces the suite, and the mutation loop grades the suite it
	// produced. Both are driven from the installed files alone.
	for _, name := range skillOrder() {
		commands := transcriptOf(t, installedSkill(t, root, name))
		if len(commands) == 0 {
			t.Fatalf("the installed %s skill embeds no executable transcript", name)
		}

		for _, command := range commands {
			runTranscriptCommand(t, command, module)
		}
	}
}

// TestASeededWrongFlagInTheSkillTurnsTheGateRed proves the oracle reads the
// instructions: a flag this binary does not have, seeded into the installed
// text, must make the walkthrough fail.
func TestASeededWrongFlagInTheSkillTurnsTheGateRed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := skill.Install(root, skill.AgentGeneric, "test", false); err != nil {
		t.Fatalf("skill install: %v", err)
	}

	installed := installedSkill(t, root, skill.NameCharacterise)
	seedWrongFlag(t, installed)

	module := walkthroughFixture(t)
	failed := false

	for _, command := range transcriptOf(t, installed) {
		stderr := bytes.Buffer{}
		if run(append(command, module), "test", &bytes.Buffer{}, &stderr) == report.ExitOperational {
			failed = true
		}
	}

	if !failed {
		t.Fatal("a seeded wrong flag left the walkthrough green, so the gate is not " +
			"reading the installed instructions")
	}
}

// skillOrder is the order the walkthrough drives the two loops in: the
// characterisation loop writes the suite, and the mutation loop grades it.
func skillOrder() []skill.Name {
	return []skill.Name{skill.NameCharacterise, skill.NameMutation}
}

// runTranscriptCommand executes one transcript line against the fixture.
func runTranscriptCommand(t *testing.T, command []string, module string) {
	t.Helper()

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	// The transcript writes the module path as `.`; the fixture lives
	// elsewhere, so the last argument is replaced rather than the working
	// directory changed, which would not be safe under a parallel suite.
	arguments := append(append([]string{}, command[:len(command)-1]...), module)

	code := run(arguments, "test", &stdout, &stderr)
	if code != report.ExitClean && code != report.ExitFindings {
		t.Fatalf("the installed walkthrough's %q exited %d: %s",
			strings.Join(command, " "), code, stderr.String())
	}
}

// transcriptOf extracts the fenced transcript blocks from an installed skill,
// in order.
func transcriptOf(t *testing.T, path string) [][]string {
	t.Helper()

	commands := [][]string{}
	content := readInstalled(t, path)
	inside := false

	for line := range strings.SplitSeq(content, "\n") {
		switch {
		case strings.HasPrefix(line, transcriptFence):
			inside = true
		case inside && strings.HasPrefix(line, "```"):
			inside = false
		case inside && strings.TrimSpace(line) != "":
			commands = append(commands, strings.Fields(line))
		default:
		}
	}

	return commands
}

// readInstalled reads an installed skill file from the test's own root.
func readInstalled(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // a test-owned install root.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(content)
}

func installedSkill(t *testing.T, root string, name skill.Name) string {
	t.Helper()

	relative, err := skill.TargetPath(skill.AgentGeneric, name)
	if err != nil {
		t.Fatalf("target path: %v", err)
	}

	return filepath.Join(root, relative)
}

// seedWrongFlag rewrites one flag in the installed text into one this binary
// does not have.
func seedWrongFlag(t *testing.T, path string) {
	t.Helper()

	content := readInstalled(t, path)

	seeded := strings.Replace(content, "--until-dry", "--until-damp", 1)
	if seeded == content {
		t.Fatal("the installed skill carries no flag to seed")
	}

	if err := os.WriteFile(path, []byte(seeded), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// walkthroughFixture is a module with no tests at all: the situation the
// characterisation loop exists for.
func walkthroughFixture(t *testing.T) string {
	t.Helper()

	module := t.TempDir()
	writeFile(t, filepath.Join(module, "main.tf"),
		"variable \"env\" {\n  type    = string\n  default = \"dev\"\n}\n\n"+
			"resource \"terraform_data\" \"app\" {\n  input = \"app-${var.env}\"\n}\n\n"+
			"output \"app\" {\n  value = terraform_data.app.output\n}\n\n"+
			"output \"tier\" {\n  value = var.env == \"prod\" ? \"critical\" : \"standard\"\n}\n")

	return module
}
