package skill_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/skill"
)

// The skill-install contract (M4d.1, issue #65): the never-write contract's
// fourth recorded exception, tested at the package that performs it. The one
// engine-adjacent claim — that the skill references only commands and flags
// the binary has — is asserted against the CLI usage text in
// cmd/tf-mut/cli_test.go, where the usage text lives.

const testVersion = "v9.9.9-test"

func install(t *testing.T, root, agent, version string, force bool) skill.Result {
	t.Helper()

	results := installAll(t, root, agent, version, force)

	return results[0]
}

// installAll returns every shipped skill's result, in install order.
func installAll(t *testing.T, root, agent, version string, force bool) []skill.Result {
	t.Helper()

	results, err := skill.Install(root, agent, version, force)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if len(results) != len(skill.Names()) {
		t.Fatalf("installed %d skills, want %d", len(results), len(skill.Names()))
	}

	return results
}

func installedPath(t *testing.T, root, agent string) string {
	t.Helper()

	relative, err := skill.TargetPath(agent, skill.NameMutation)
	if err != nil {
		t.Fatalf("target path: %v", err)
	}

	return filepath.Join(root, relative)
}

func TestAFreshInstallPlacesTheSkillAtTheDocumentedPath(t *testing.T) {
	t.Parallel()

	for agent, wanted := range map[string]string{
		skill.AgentClaude:  ".claude/skills/tf-mut-mutation/SKILL.md",
		skill.AgentGeneric: ".agents/skills/tf-mut-mutation.md",
	} {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()

			result := install(t, root, agent, testVersion, false)
			if result.Outcome != skill.OutcomeInstalled {
				t.Fatalf("outcome = %s, want installed", result.Outcome)
			}

			if filepath.ToSlash(result.Path) != wanted {
				t.Fatalf("path = %s, want %s", result.Path, wanted)
			}

			content, err := os.ReadFile(installedPath(t, root, agent))
			if err != nil {
				t.Fatalf("reading the installed skill: %v", err)
			}

			if !strings.Contains(string(content), testVersion) {
				t.Fatal("the installed skill does not carry the binary's version")
			}
		})
	}
}

func TestAnUnknownAgentIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := skill.Install(t.TempDir(), "cursor", testVersion, false); err == nil {
		t.Fatal("cursor is served by generic and must not be its own adapter")
	}
}

func TestASameVersionReinstallIsANoOp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, skill.AgentClaude, testVersion, false)

	before, err := os.Stat(installedPath(t, root, skill.AgentClaude))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	result := install(t, root, skill.AgentClaude, testVersion, false)
	if result.Outcome != skill.OutcomeUnchanged {
		t.Fatalf("outcome = %s, want unchanged", result.Outcome)
	}

	after, err := os.Stat(installedPath(t, root, skill.AgentClaude))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a same-version reinstall rewrote the file")
	}
}

func TestAUserEditSurvivesAReinstallUnlessForced(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, skill.AgentClaude, testVersion, false)

	target := installedPath(t, root, skill.AgentClaude)

	content, err := os.ReadFile(target) //nolint:gosec // a test-owned temporary path.
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	edited := string(content) + "\n## My local adaptation\n"
	//nolint:gosec // a test-owned temporary path.
	if writeErr := os.WriteFile(target, []byte(edited), 0o600); writeErr != nil {
		t.Fatalf("editing: %v", writeErr)
	}

	// A cross-version upgrade must not destroy the edit.
	preserved := install(t, root, skill.AgentClaude, "v10.0.0-test", false)
	if preserved.Outcome != skill.OutcomePreservedEdit {
		t.Fatalf("outcome = %s, want preserved-user-edit", preserved.Outcome)
	}

	kept, err := os.ReadFile(target) //nolint:gosec // a test-owned temporary path.
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}

	if string(kept) != edited {
		t.Fatal("the preserved file changed anyway")
	}

	forced := install(t, root, skill.AgentClaude, "v10.0.0-test", true)
	if forced.Outcome != skill.OutcomeForced {
		t.Fatalf("outcome = %s, want forced", forced.Outcome)
	}

	replaced, err := os.ReadFile(target) //nolint:gosec // a test-owned temporary path.
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}

	if strings.Contains(string(replaced), "My local adaptation") {
		t.Fatal("--force did not replace the edited file")
	}
}

func TestACrossVersionUpgradeReplacesAnUnmodifiedInstallAndReportsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, skill.AgentClaude, testVersion, false)

	result := install(t, root, skill.AgentClaude, "v10.0.0-test", false)
	if result.Outcome != skill.OutcomeUpgraded {
		t.Fatalf("outcome = %s, want upgraded", result.Outcome)
	}

	content, err := os.ReadFile(installedPath(t, root, skill.AgentClaude))
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	if !strings.Contains(string(content), "v10.0.0-test") {
		t.Fatal("the upgrade did not replace the version stamp")
	}
}

func TestTheContentCarriesTheContractedRules(t *testing.T) {
	t.Parallel()

	// Markdown wraps at the column limit, so the verbatim quotes are checked
	// over whitespace-normalised text.
	content := strings.Join(strings.Fields(skill.Content(skill.NameMutation)), " ")

	// The agent-integration contract, quoted verbatim by issue #65.
	if !strings.Contains(content,
		"hand-write assertions the harvest can generate") ||
		!strings.Contains(content,
			"harvested assertions are evidence and hand-written ones are guesses") {
		t.Fatal("the skill lost the no-hand-written-assertions rule")
	}

	// The survivor-diagnosis decision tree names every actionable diagnosis.
	for _, diagnosis := range []string{
		"no-assertion", "weak-assertion", "unasserted",
		"indeterminate-unknown-values", "indeterminate-volatility",
		"NoCoverage", "StructurallyUnassertable",
	} {
		if !strings.Contains(content, diagnosis) {
			t.Fatalf("the decision tree is missing %s", diagnosis)
		}
	}
}

// TestTheSkillTeachesOnlyDocumentedFlags is the shape half of the
// self-consistency assertion: every `--flag` the skill mentions must appear in
// the binary's usage text, which cli_test cross-checks from the cmd package.
func TestTheSkillFlagsAreWellFormed(t *testing.T) {
	t.Parallel()

	flags := regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(skill.Content(skill.NameMutation), -1)
	if len(flags) == 0 {
		t.Fatal("the skill teaches no flags at all, which cannot be the loop")
	}
}
