package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/skill"
)

const (
	helpSingleDashFlag = "-help"
	unknownFlag        = "--unknown"
)

// TestReferencePageMatchesCommittedPage is technical-documentation ticket
// T6's drift guard: docs/reference/cli.md is rendered from the same
// description table as --help, and this test fails when the committed page
// is not byte-equal to what the renderer produces. Regenerate with
// `just reference`.
func TestReferencePageMatchesCommittedPage(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile("../../docs/reference/cli.md")
	if err != nil {
		t.Fatalf("reading the committed reference page: %v", err)
	}

	rendered := renderReferencePage()
	if rendered == "" {
		t.Fatal("the renderer produced an empty reference page")
	}

	if string(committed) != rendered {
		t.Fatalf("docs/reference/cli.md drifts from the renderer: run `just reference` "+
			"and commit the result (committed %d bytes, rendered %d bytes, "+
			"first difference at byte %d)",
			len(committed), len(rendered), firstDifference(string(committed), rendered))
	}
}

func TestReferenceFlagTablesPreserveTheirThreeColumns(t *testing.T) {
	t.Parallel()

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}
	if code := run([]string{generateReferenceFlag}, "test", &stdout, &stderr); code != exitSuccess {
		t.Fatalf("reference generation exited %d: %s", code, stderr.String())
	}

	agentRows := 0

	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if !strings.HasPrefix(line, "| `--") {
			continue
		}

		cells := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
		if len(cells) != 5 {
			t.Fatalf("flag row has %d columns, want 3: %s", len(cells)-2, line)
		}

		for i, cell := range cells {
			cells[i] = strings.ReplaceAll(strings.TrimSpace(cell), "\x00", "|")
		}

		if cells[1] == "`--agent claude|generic`" {
			agentRows++

			want := []string{"", "`--agent claude|generic`", "Target agent for the installed skills", "`\"claude\"`", ""}
			if !slices.Equal(cells, want) {
				t.Fatalf("agent row = %q, want %q", cells, want)
			}
		}
	}

	if agentRows != 1 {
		t.Fatalf("reference contains %d agent rows, want 1", agentRows)
	}
}

// firstDifference names the byte where two documents part, so a drift report
// points at the line instead of dumping two pages.
func firstDifference(left, right string) int {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return i
		}
	}

	return min(len(left), len(right))
}

// TestExplicitHelpExitsZeroAndUsageErrorsExitTwo is T6's one behaviour change:
// asking for help exits 0, while a misuse still exits 2. The engine's own
// exit codes are untouched: 0 and 1 keep meaning what the report says.
func TestExplicitHelpExitsZeroAndUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{helpLongFlag},
		{helpShortFlag},
		{runCommand, helpShortFlag},
		{runCommand, helpLongFlag},
		{runCommand, "--test-directory", "tests", "--" + jobsFlag, "1", helpShortFlag},
		{previewCommand, helpLongFlag},
		{suggestCommand, helpShortFlag},
		{characteriseCommand, helpLongFlag},
		{todosCommand, helpShortFlag},
		{curateCommand, helpLongFlag},
		{skillCommand, helpLongFlag},
		{skillCommand, installCommand, helpLongFlag},
		{versionCommand, helpLongFlag},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			t.Parallel()

			stdout := bytes.Buffer{}
			stderr := bytes.Buffer{}

			if code := run(args, "test", &stdout, &stderr); code != exitSuccess {
				t.Fatalf("exit code = %d, want %d for %q: %s",
					code, exitSuccess, args, stderr.String())
			}

			if stdout.String() == "" {
				t.Fatalf("explicit help for %q printed nothing", args)
			}

			if stderr.String() != "" {
				t.Fatalf("explicit help for %q wrote to stderr: %q", args, stderr.String())
			}
		})
	}

	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{runCommand, "--bogus"},
		{runCommand, t.TempDir(), "--" + jobsFlag, "1"},
	} {
		t.Run("misuse-"+strings.Join(args, "-"), func(t *testing.T) {
			t.Parallel()

			if code := run(args, "test", &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
				t.Fatalf("exit code = %d, want %d for %q", code, exitUsage, args)
			}
		})
	}
}

func TestHelpRespectsEngineFlagParsing(t *testing.T) {
	t.Parallel()

	module := t.TempDir()

	for _, command := range []string{
		runCommand, previewCommand, suggestCommand, characteriseCommand, todosCommand, curateCommand,
	} {
		for _, help := range []string{helpShortFlag, helpLongFlag, helpSingleDashFlag} {
			for _, test := range []struct {
				args []string
				want string
			}{
				{[]string{"--test-directory", help, unknownFlag}, "flag provided but not defined"},
				{[]string{"--" + jobsFlag, help}, "invalid value"},
				{[]string{unknownFlag, help}, "flag provided but not defined"},
				{[]string{module, help}, errTrailingArguments.Error()},
				{[]string{"--", module, help}, errTrailingArguments.Error()},
				{[]string{"--", help, module}, errTrailingArguments.Error()},
			} {
				args := append([]string{command}, test.args...)
				stdout := bytes.Buffer{}
				stderr := bytes.Buffer{}

				if code := run(args, "test", &stdout, &stderr); code != exitUsage {
					t.Fatalf("%q exited %d, want %d: %s", args, code, exitUsage, stderr.String())
				}

				if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
					t.Fatalf("%q: stdout = %q, stderr = %q; want only error %q",
						args, stdout.String(), stderr.String(), test.want)
				}
			}
		}
	}
}

func TestHelpRespectsVersionFlagParsing(t *testing.T) {
	t.Parallel()

	for _, command := range []string{versionCommand, versionFlag} {
		for _, help := range []string{helpShortFlag, helpLongFlag, helpSingleDashFlag} {
			for _, args := range [][]string{
				{command, "--", help},
				{command, "positional", help},
			} {
				stdout := bytes.Buffer{}
				stderr := bytes.Buffer{}

				if code := run(args, "test", &stdout, &stderr); code != exitUsage ||
					stdout.Len() != 0 || !strings.Contains(stderr.String(), "takes no arguments") {
					t.Fatalf("%q: exit = %d, stdout = %q, stderr = %q",
						args, code, stdout.String(), stderr.String())
				}
			}

			stdout := bytes.Buffer{}
			stderr := bytes.Buffer{}
			args := []string{command, unknownFlag, help}

			if code := run(args, "test", &stdout, &stderr); code != exitUsage ||
				stdout.Len() != 0 || !strings.Contains(stderr.String(), "flag provided but not defined") {
				t.Fatalf("%q: exit = %d, stdout = %q, stderr = %q",
					args, code, stdout.String(), stderr.String())
			}
		}
	}
}

//nolint:paralleltest // t.Chdir changes the process working directory and forbids parallel execution.
func TestHelpRespectsSkillFlagParsing(t *testing.T) {
	t.Chdir(t.TempDir())

	relative, err := skill.TargetPath(skill.AgentGeneric, skill.NameMutation)
	if err != nil {
		t.Fatal(err)
	}

	for _, help := range []string{helpShortFlag, helpLongFlag, helpSingleDashFlag} {
		stdout := bytes.Buffer{}
		stderr := bytes.Buffer{}
		args := []string{skillCommand, installCommand, "--" + agentFlag, skill.AgentGeneric, "--path", help}

		if code := run(args, "test", &stdout, &stderr); code != exitSuccess || stderr.Len() != 0 {
			t.Fatalf("%q: exit = %d, stderr = %q", args, code, stderr.String())
		}

		if _, err := os.Stat(filepath.Join(help, relative)); err != nil {
			t.Fatalf("%q did not install into the --path value: %v", args, err)
		}

		for _, args := range [][]string{
			{skillCommand, "unknown", help},
			{skillCommand, "--", help},
			{skillCommand, installCommand, unknownFlag, help},
			{skillCommand, installCommand, "--" + agentFlag, help},
			{skillCommand, installCommand, "--" + agentFlag, skill.AgentGeneric, "--", help},
			{skillCommand, installCommand, "positional", help},
		} {
			stdout.Reset()
			stderr.Reset()

			if code := run(args, "test", &stdout, &stderr); code != exitUsage || stdout.Len() != 0 {
				t.Fatalf("%q: exit = %d, stdout = %q, stderr = %q",
					args, code, stdout.String(), stderr.String())
			}
		}
	}

	if _, err := os.Stat(relative); !os.IsNotExist(err) {
		t.Fatalf("invalid skill arguments installed into the working directory: %v", err)
	}
}

func TestForceHelpDescribesTheCommandsOverwritePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{characteriseCommand, helpShortFlag}, "Replace generated files nobody has edited"},
		{[]string{skillCommand, helpShortFlag}, "Overwrite user-edited skill files"},
		{[]string{skillCommand, installCommand, helpShortFlag}, "Overwrite user-edited skill files"},
	} {
		stdout := bytes.Buffer{}
		stderr := bytes.Buffer{}

		if code := run(test.args, "test", &stdout, &stderr); code != exitSuccess {
			t.Fatalf("%q exited %d: %s", test.args, code, stderr.String())
		}

		if !strings.Contains(stdout.String(), test.want) {
			t.Fatalf("%q help lacks overwrite policy %q: %s", test.args, test.want, stdout.String())
		}
	}
}

func TestTierHelpShowsItsRequiredValue(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{helpLongFlag},
		{runCommand, helpShortFlag},
		{previewCommand, helpShortFlag},
		{suggestCommand, helpShortFlag},
		{generateReferenceFlag},
	} {
		stdout := bytes.Buffer{}
		stderr := bytes.Buffer{}

		if code := run(args, "test", &stdout, &stderr); code != exitSuccess {
			t.Fatalf("%q exited %d: %s", args, code, stderr.String())
		}

		if !strings.Contains(stdout.String(), "--tier LEVEL") {
			t.Fatalf("%q help lacks the --tier value: %s", args, stdout.String())
		}
	}
}

// TestPerCommandHelpCarriesItsOwnSurface keeps every -h honest: usage line,
// synopsis, arguments, one example, the three exit codes, and exactly the
// flags the parser accepts for that command — no more, no fewer.
func TestPerCommandHelpCarriesItsOwnSurface(t *testing.T) {
	t.Parallel()

	for _, doc := range commandDocs {
		t.Run(doc.name, func(t *testing.T) {
			t.Parallel()

			text := commandHelp(doc.name)

			for _, required := range []string{
				"usage: tf-mut " + doc.name,
				doc.synopsis,
				doc.arguments,
				doc.example,
				"Exit codes:",
			} {
				if !strings.Contains(text, required) {
					t.Errorf("%s help lacks %q", doc.name, required)
				}
			}

			names := applicableFlags(doc.name)
			for _, name := range names {
				if !mentionsFlag(text, name) {
					t.Errorf("%s help does not document --%s, which it accepts", doc.name, name)
				}
			}

			for flag := range flagDocs {
				if mentionsFlag(text, flag) && !slices.Contains(names, flag) {
					t.Errorf("%s help documents --%s, which the parser refuses", doc.name, flag)
				}
			}
		})
	}
}

// mentionsFlag reports whether the help text documents a flag: the name as
// its own line entry, so --write does not match --write-baseline and prose
// mentions of an accepted flag do not count as documenting it.
func mentionsFlag(text, name string) bool {
	return strings.Contains(text, "\n  --"+name+" ")
}

// TestHelpTableCoversEveryDeclaredFlag holds the description table to the
// flag set in both directions: a declared flag with no table entry renders no
// help, and a table entry with no declared flag renders a lie.
func TestHelpTableCoversEveryDeclaredFlag(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	liveFlags().VisitAll(func(found *flag.Flag) { declared[found.Name] = true })
	liveSkillFlags().VisitAll(func(found *flag.Flag) { declared[found.Name] = true })

	for name := range declared {
		if _, documented := flagDocs[name]; !documented {
			t.Errorf("declared flag --%s has no help-table entry", name)
		}
	}

	for name := range flagDocs {
		if !declared[name] {
			t.Errorf("help-table entry --%s names no declared flag", name)
		}
	}

	// The top-level help lists every engine flag exactly once. The install
	// flags stay out: agent and path belong to `skill install` alone, and
	// force shares its spelling with the engine flag the groups already list.
	grouped := map[string]int{}
	for _, group := range flagGroups {
		for _, name := range group.flags {
			grouped[name]++
		}
	}

	for name, count := range grouped {
		if count != 1 {
			t.Errorf("top-level help lists --%s %d times, want once", name, count)
		}
	}

	liveFlags().VisitAll(func(found *flag.Flag) {
		if grouped[found.Name] != 1 {
			t.Errorf("declared flag --%s appears %d times in the top-level help, want once",
				found.Name, grouped[found.Name])
		}
	})
}

// TestTheJobsDefaultIsRenderedFromTheCode pins ticket T6's sixth acceptance:
// the --jobs default text is the engine's own description of its policy, not
// a retyped copy of it.
func TestTheJobsDefaultIsRenderedFromTheCode(t *testing.T) {
	t.Parallel()

	if !strings.Contains(usage, engine.DefaultJobsDescription) {
		t.Fatalf("the top-level help does not render the engine's jobs default %q",
			engine.DefaultJobsDescription)
	}

	if !strings.Contains(commandHelp(runCommand), engine.DefaultJobsDescription) {
		t.Fatalf("the run help does not render the engine's jobs default %q",
			engine.DefaultJobsDescription)
	}

	if !strings.Contains(renderReferencePage(), engine.DefaultJobsDescription) {
		t.Fatalf("the reference page does not render the engine's jobs default %q",
			engine.DefaultJobsDescription)
	}
}

// TestRequestedHelpMatchesTheUsageValue keeps --help honest against the value
// the audit tests read: the skill and vocabulary guards pin the binary's
// surface through `usage`, so --help must print exactly that.
func TestRequestedHelpMatchesTheUsageValue(t *testing.T) {
	t.Parallel()

	stdout := bytes.Buffer{}

	if code := run([]string{helpLongFlag}, "test", &stdout, &bytes.Buffer{}); code != exitSuccess {
		t.Fatalf("exit code = %d, want %d", code, exitSuccess)
	}

	if stdout.String() != usage+"\n" {
		t.Fatal("`tf-mut --help` does not print the usage value the audit tests pin")
	}

	// The report's own exit codes are the ones the help text promises: help
	// rendering must not renumber them.
	if report.ExitClean != 0 || report.ExitFindings != 1 || report.ExitOperational != 2 {
		t.Fatalf("the report exit codes moved: clean=%d findings=%d operational=%d",
			report.ExitClean, report.ExitFindings, report.ExitOperational)
	}
}
