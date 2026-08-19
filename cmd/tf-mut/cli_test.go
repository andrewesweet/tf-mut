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
				characteriseCommand, resumeFlag, reporterFlag, reporterJSON)))
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

	code := run([]string{todosCommand, ".", resumeFlag}, "test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	for _, expected := range []string{resumeFlag, beforeThePath} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}

// transcriptFence marks the fenced blocks the end-of-MVP gate executes.
//
// The fences are matched exactly rather than by prefix: `tf-mut-transcript` is
// a prefix of `tf-mut-transcript-todo`, so a prefix match would fold the
// judgement-point block into the main walkthrough and run it against a module
// that has no judgement point to answer.
const (
	transcriptFence = "```tf-mut-transcript"
	todoFence       = "```tf-mut-transcript-todo"
)

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
		commands := transcriptOf(t, installedSkill(t, root, name), transcriptFence)
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
// text, must make the walkthrough fail — and fail *for that reason*.
//
// The test is built so that neutering the seed cannot leave it green. It runs
// the transcript twice, over two modules: unseeded, where every command must
// succeed, and seeded, where some command must be refused *by name*. A no-op
// seed makes the second phase find no such refusal and fail; a refusal
// arriving from any other operational path fails the name check.
//
// The first version of this test asserted only a non-zero exit code, and
// appended the module path to a transcript line that already ended in `.` —
// so the CLI refused two positional arguments and the test passed with the
// seed removed entirely. The gate that made the walkthrough falsifiable was
// itself unfalsifiable.
func TestASeededWrongFlagInTheSkillTurnsTheGateRed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := skill.Install(root, skill.AgentGeneric, "test", false); err != nil {
		t.Fatalf("skill install: %v", err)
	}

	installed := installedSkill(t, root, skill.NameCharacterise)

	// Unseeded first, over one module in order — a seed proves nothing about a
	// walkthrough that was already failing, and the transcript is a sequence:
	// `characterise --write` produces the suite `curate` then grades.
	clean := walkthroughFixture(t)
	for _, command := range transcriptOf(t, installed, transcriptFence) {
		runTranscriptCommand(t, command, clean)
	}

	seedWrongFlag(t, installed, transcriptFence, "--until-dry", seededFlag)

	seeded := walkthroughFixture(t)
	named := false

	for _, command := range transcriptOf(t, installed, transcriptFence) {
		stderr := bytes.Buffer{}

		code := run(substituteModule(command, seeded), "test", &bytes.Buffer{}, &stderr)
		if code != report.ExitOperational {
			continue
		}

		// Go's flag package prints the single-dash form, so the assertion is
		// on the flag's name rather than on the spelling the skill used.
		if strings.Contains(stderr.String(), strings.TrimPrefix(seededFlag, "-")) {
			named = true
		}
	}

	if !named {
		t.Fatalf("no transcript command was refused for %s, so the gate is not reading "+
			"the installed instructions", seededFlag)
	}
}

// seededFlag is the flag this binary does not have, seeded into the installed
// skill so the walkthrough has to notice it.
const seededFlag = "--until-damp"

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

	code := run(substituteModule(command, module), "test", &stdout, &stderr)
	if code != report.ExitClean && code != report.ExitFindings {
		t.Fatalf("the installed walkthrough's %q exited %d: %s",
			strings.Join(command, " "), code, stderr.String())
	}

	// A walkthrough every step of which reports *incomplete* is a walkthrough
	// that parses and runs, not one that converges — and the loop's whole
	// claim is that it converges. The write step is where the claim lands, so
	// it is the step that has to come back clean.
	if strings.Contains(strings.Join(command, " "), writeFlag) && code != report.ExitClean {
		t.Fatalf("the installed walkthrough's %q exited %d: the loop it teaches does not "+
			"converge on this module: %s", strings.Join(command, " "), code, stderr.String())
	}
}

// writeFlag is the transcript step that has to succeed outright.
const writeFlag = "--write"

// resumeFlag is the artefact-answer step's flag, spelled in three cases.
const resumeFlag = "--resume"

// substituteModule points a transcript line at the fixture.
//
// The transcript writes the module path as `.`, and the fixture lives
// elsewhere, so the last argument is *replaced* rather than appended — an
// append produces two positional arguments, which the CLI refuses by name,
// and a refusal that arrives whatever the command said is a refusal that
// proves nothing.
func substituteModule(command []string, module string) []string {
	return append(append([]string{}, command[:len(command)-1]...), module)
}

// transcriptOf extracts the fenced transcript blocks from an installed skill,
// in order.
func transcriptOf(t *testing.T, path, fence string) [][]string {
	t.Helper()

	commands := [][]string{}
	content := readInstalled(t, path)
	inside := false

	for line := range strings.SplitSeq(content, "\n") {
		switch {
		case strings.TrimSpace(line) == fence:
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

// seedWrongFlag rewrites one flag of the installed *transcript* into one this
// binary does not have.
//
// The transcript specifically, not the first match in the file: the skill's
// prose names `--until-dry` several times before the fenced block does, so a
// whole-file replace seeds a sentence the gate never executes and proves
// nothing about the instructions it does.
func seedWrongFlag(t *testing.T, path, fence, flag, seeded string) {
	t.Helper()

	content := readInstalled(t, path)

	start := strings.Index(content, fence)
	if start < 0 {
		t.Fatal("the installed skill embeds no transcript to seed")
	}

	replaced := content[:start] + strings.Replace(content[start:], flag, seeded, 1)
	if replaced == content {
		t.Fatal("the installed transcript carries no flag to seed")
	}

	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
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

// TestCurateIsWiredThroughTheCommandLine covers the third new command: the
// refusal a partial population earns can only be asserted from the shell if
// the shell actually reaches the engine with the command set.
func TestCurateIsWiredThroughTheCommandLine(t *testing.T) {
	t.Parallel()

	module := fixture(t)
	stderr := bytes.Buffer{}

	code := run([]string{curateCommand, "--sample", "50", noCacheFlag, module},
		"test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	if !strings.Contains(stderr.String(), "false finding") {
		t.Fatalf("the refusal did not come from curate's population posture: %s", stderr.String())
	}
}

// TestCurateRefusesArgumentsAfterTheModulePath keeps the round-3 ordering
// repair asserted by name for every command the milestone added.
func TestCurateRefusesArgumentsAfterTheModulePath(t *testing.T) {
	t.Parallel()

	stderr := bytes.Buffer{}

	code := run([]string{curateCommand, ".", noCacheFlag}, "test", &bytes.Buffer{}, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	for _, expected := range []string{noCacheFlag, beforeThePath} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("the refusal does not carry %q: %s", expected, stderr.String())
		}
	}
}

// TestACharacterisationFlagIsRefusedByAGradingCommand keeps the flag surface
// honest per command.
//
// `--write`, `--force`, `--pin`, `--until-dry`, `--answer` and `--resume` are
// declared on the flag set every command shares, so `run`, `preview` and
// `suggest` accepted and ignored them. Two of those name write behaviour and a
// third is validated under `characterise` and was not under anything else.
func TestACharacterisationFlagIsRefusedByAGradingCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		flag    string
	}{
		{runCommand, writeFlag},
		{runCommand, "--force"},
		{runCommand, "--pin=nonsense"},
		{previewCommand, "--until-dry"},
		{suggestCommand, resumeFlag},
		{curateCommand, "--apply=sug-1"},
		{curateCommand, "--until-dry"},
		{todosCommand, writeFlag},
	}

	for _, testCase := range cases {
		t.Run(testCase.command+testCase.flag, func(t *testing.T) {
			t.Parallel()

			stderr := bytes.Buffer{}

			code := run([]string{testCase.command, testCase.flag, t.TempDir()},
				"test", &bytes.Buffer{}, &stderr)
			if code != report.ExitOperational {
				t.Fatalf("exit code = %d, want %d: %s",
					code, report.ExitOperational, stderr.String())
			}

			if !strings.Contains(stderr.String(), "not a "+testCase.command+" flag") {
				t.Fatalf("the refusal does not name the command: %s", stderr.String())
			}
		})
	}
}

// TestAReporterThatCannotCarryACharacterisationIsRefused closes the other half
// of the same shape.
//
// The generated suite lives in `characterisation.files`, which the SARIF, MTE,
// HTML, JUnit and Markdown adapters do not carry, so `characterise --reporter
// markdown` wrote nothing, returned no suite and exited as though it had
// succeeded.
func TestAReporterThatCannotCarryACharacterisationIsRefused(t *testing.T) {
	t.Parallel()

	for _, reporter := range []string{"markdown", "sarif", "junit", "html", "mte"} {
		t.Run(reporter, func(t *testing.T) {
			t.Parallel()

			stderr := bytes.Buffer{}

			code := run([]string{characteriseCommand, "--reporter", reporter, t.TempDir()},
				"test", &bytes.Buffer{}, &stderr)
			if code != report.ExitOperational {
				t.Fatalf("exit code = %d, want %d: %s",
					code, report.ExitOperational, stderr.String())
			}

			if !strings.Contains(stderr.String(), "cannot carry a characterisation") {
				t.Fatalf("the refusal does not say why: %s", stderr.String())
			}
		})
	}
}

// TestTheInstalledWalkthroughDrainsAJudgementPoint executes the step of the
// shipped loop that the main transcript cannot.
//
// Step 3 — answer a judgement point and re-plan — is the step the whole
// characterisation loop turns on, and it was the one step of the documented
// sequence that nothing executed: `--answer` takes a content-derived
// identifier, which a static transcript cannot spell. The identifier is
// resolved the way a reader resolves it, out of the `todos --reporter json`
// output printed on the line before, so the instructions stay the thing under
// test rather than a script kept beside them.
func TestTheInstalledWalkthroughDrainsAJudgementPoint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := skill.Install(root, skill.AgentGeneric, "test", false); err != nil {
		t.Fatalf("skill install: %v", err)
	}

	commands := transcriptOf(t, installedSkill(t, root, skill.NameCharacterise), todoFence)
	if len(commands) == 0 {
		t.Fatal("the installed skill embeds no judgement-point transcript")
	}

	module := judgementPointFixture(t)
	written := drainTranscript(t, commands, module)

	if !written {
		t.Fatal("the judgement-point walkthrough never reached a clean write, so the loop " +
			"it teaches does not close on a module with an open judgement point")
	}

	target := filepath.Join(module, "tests", "characterise_defaults.tftest.hcl")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the answered walkthrough wrote no suite: %v", err)
	}
}

// TestASeededWrongFlagInTheJudgementPointWalkthroughTurnsItRed keeps the new
// block as falsifiable as the one beside it: a flag this binary does not have,
// seeded into the installed text, must make the walkthrough fail by name.
func TestASeededWrongFlagInTheJudgementPointWalkthroughTurnsItRed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := skill.Install(root, skill.AgentGeneric, "test", false); err != nil {
		t.Fatalf("skill install: %v", err)
	}

	installed := installedSkill(t, root, skill.NameCharacterise)
	seedWrongFlag(t, installed, todoFence, "--answer", seededAnswerFlag)

	commands := transcriptOf(t, installed, todoFence)
	module := judgementPointFixture(t)
	named := false

	for _, command := range commands {
		stdout := bytes.Buffer{}
		stderr := bytes.Buffer{}

		code := run(substituteModule(command, module), "test", &stdout, &stderr)
		if code == report.ExitOperational &&
			strings.Contains(stderr.String(), strings.TrimPrefix(seededAnswerFlag, "-")) {
			named = true
		}
	}

	if !named {
		t.Fatalf("no command of the judgement-point transcript was refused for %s, so the "+
			"gate is not reading the installed instructions", seededAnswerFlag)
	}
}

// seededAnswerFlag is the judgement-point block's equivalent of seededFlag.
const seededAnswerFlag = "--anwser"

// todoPlaceholder is what the transcript writes where the identifier goes.
const todoPlaceholder = "<todo-id>"

// drainTranscript runs the judgement-point transcript, resolving the
// identifier out of the `todos` output as it goes, and reports whether the
// write step came back clean.
func drainTranscript(t *testing.T, commands [][]string, module string) bool {
	t.Helper()

	identifier := ""
	written := false

	for _, command := range commands {
		resolved := substituteTodo(t, substituteModule(command, module), identifier)

		stdout := bytes.Buffer{}
		stderr := bytes.Buffer{}

		code := run(resolved, "test", &stdout, &stderr)
		if code != report.ExitClean && code != report.ExitFindings {
			t.Fatalf("the installed walkthrough's %q exited %d: %s",
				strings.Join(command, " "), code, stderr.String())
		}

		if identifier == "" {
			identifier = firstTodoIdentifier(stdout.String())
		}

		if strings.Contains(strings.Join(command, " "), writeFlag) {
			if code != report.ExitClean {
				t.Fatalf("the walkthrough's write step exited %d: %s", code, stderr.String())
			}

			written = true
		}
	}

	return written
}

// substituteTodo fills the identifier placeholder in, and refuses a command
// that still carries one — an unresolved placeholder reaching the binary would
// be refused for the wrong reason and prove nothing.
func substituteTodo(t *testing.T, command []string, identifier string) []string {
	t.Helper()

	resolved := make([]string, 0, len(command))

	for _, argument := range command {
		if !strings.Contains(argument, todoPlaceholder) {
			resolved = append(resolved, argument)

			continue
		}

		if identifier == "" {
			t.Fatalf("the transcript reads %s before any judgement point was reported",
				todoPlaceholder)
		}

		resolved = append(resolved, strings.ReplaceAll(argument, todoPlaceholder, identifier))
	}

	return resolved
}

// reportedTodo is one judgement point as a JSON report publishes it.
type reportedTodo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// reportedCharacterisation is the block the identifier is read out of.
type reportedCharacterisation struct {
	Todos []reportedTodo `json:"todos"`
}

// reportedRun is the reader's own view of a JSON report: exactly the two
// fields resolving `<todo-id>` needs, decoded the way a caller of this tool
// would decode them.
type reportedRun struct {
	Characterisation reportedCharacterisation `json:"characterisation"`
}

// firstTodoIdentifier reads the first open judgement point's identifier out of
// a JSON report, or returns empty where the output carries none.
func firstTodoIdentifier(document string) string {
	decoded := reportedRun{Characterisation: reportedCharacterisation{Todos: nil}}

	if json.Unmarshal([]byte(document), &decoded) != nil {
		return ""
	}

	for _, todo := range decoded.Characterisation.Todos {
		if todo.Status == "open" {
			return todo.ID
		}
	}

	return ""
}

// judgementPointFixture is a module the deterministic pipeline cannot resolve:
// a required input whose constraint states a property rather than a value.
func judgementPointFixture(t *testing.T) string {
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
