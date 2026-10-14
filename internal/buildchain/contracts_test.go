package buildchain_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

const (
	ciWorkflowPath     = ".github/workflows/ci.yml"
	codeQLWorkflowPath = ".github/workflows/codeql.yml"
	minimumToolCount   = 1
	mutationDiffJobID  = "mutation-diff"
	mutationDiffName   = "mutate-diff"
	securityName       = "security"
	pushEventName      = "push"
	eventBaseEnv       = "EVENT_BASE"
	eventNameEnv       = "GITHUB_EVENT_NAME"
	miseExecCommand    = "exec"
	justCommand        = "just"
)

type workflowContract struct {
	On          map[string]any         `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Uses        string            `yaml:"uses"`
	Env         map[string]string `yaml:"env"`
	Permissions map[string]string `yaml:"permissions"`
	Strategy    workflowStrategy  `yaml:"strategy"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflowStrategy struct {
	Matrix workflowMatrix `yaml:"matrix"`
}

type workflowMatrix struct {
	Include []map[string]string `yaml:"include"`
}

type dependabotUpdate struct {
	Ecosystem string `yaml:"package-ecosystem"` //nolint:tagliatelle // Dependabot requires this YAML key.
}

type workflowStep struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

func readWorkflow(t *testing.T, path string) workflowContract {
	t.Helper()

	var workflow workflowContract
	if err := yaml.Unmarshal([]byte(readRepositoryFile(t, path)), &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	return workflow
}

func workflowPaths() []string {
	return []string{
		ciWorkflowPath, codeQLWorkflowPath,
		".github/workflows/release.yml", ".github/workflows/action-test.yml",
	}
}

func TestControlPlaneCoversEveryRepositoryLanguage(t *testing.T) {
	t.Parallel()

	controlPlane := strings.Join([]string{
		readRepositoryFile(t, "Justfile"),
		readRepositoryFile(t, "scripts/format"),
		readRepositoryFile(t, "scripts/lint-shell"),
		readRepositoryFile(t, "scripts/lint-config"),
		readRepositoryFile(t, "scripts/lint-actions"),
	}, "\n")
	for _, command := range []string{
		"shfmt",
		"bash -n",
		"shellcheck",
		"jq",
		"yamlfmt",
		"tombi",
		"actionlint",
		"zizmor",
	} {
		if !strings.Contains(controlPlane, command) {
			t.Errorf("control plane does not invoke %q", command)
		}
	}
}

func TestPublicControlPlaneRecipesExist(t *testing.T) {
	t.Parallel()

	justfile := readRepositoryFile(t, "Justfile")
	for _, recipe := range []string{
		"doctor", "tools-install", "tools-outdated", "tools-update", "providers-update",
		"deps-update", "update", "build", "fmt", "fmt-check", "lint", "lint-go",
		"lint-shell", "lint-config", "lint-actions", "lint-fix", "fix", "typecheck",
		"mod-check", "test", "test-property", "test-random", "test-race",
		"test-integration", "test-performance", "fuzz", "fuzz-all", mutationDiffName,
		"mutate", securityName, "agent-check", "ci", "ci-full",
	} {
		declaration := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(recipe) + `(?: [^:]*)?:`)
		if !declaration.MatchString(justfile) {
			t.Errorf("Justfile does not declare public recipe %q", recipe)
		}
	}
}

func TestDoctorVerifiesLiveProviderChain(t *testing.T) {
	t.Parallel()

	doctor := readRepositoryFile(t, "scripts/doctor")
	if !strings.Contains(doctor, "go run ./internal/buildchain/cmd verify-providers") {
		t.Error("doctor does not verify the live provider mirror against its lock and allowlist")
	}
}

func TestCodeQLCoversEverySupportedLanguageWithMaximumBuiltInSuite(t *testing.T) {
	t.Parallel()

	workflow := readWorkflow(t, codeQLWorkflowPath)
	analyze := workflow.Jobs["analyze"]
	languages := make([]string, 0, len(analyze.Strategy.Matrix.Include))
	for _, row := range analyze.Strategy.Matrix.Include {
		languages = append(languages, row["language"])
	}
	slices.Sort(languages)
	if !slices.Equal(languages, []string{"actions", "go"}) {
		t.Errorf("CodeQL languages = %q, want actions and go", languages)
	}
	if analyze.Permissions["security-events"] != "write" {
		t.Error("CodeQL cannot upload results")
	}
	foundInit := false
	for _, step := range analyze.Steps {
		action, _, _ := strings.Cut(step.Uses, "@")
		if action == "github/codeql-action/init" {
			foundInit = true
			if step.With["queries"] != "security-and-quality" {
				t.Error("CodeQL does not select the maximum built-in suite")
			}
		}
	}
	if !foundInit {
		t.Error("CodeQL has no initialization step")
	}
	if _, unsafe := workflow.On["pull_request_target"]; unsafe {
		t.Error("CodeQL workflow must not use pull_request_target")
	}
}

func TestWorkflowSecurityContract(t *testing.T) {
	t.Parallel()
	assertWorkflowCoreSecurity(t)
	assertCIWorkflowContract(t)
	assertWorkflowMisePins(t)
	assertDependabotUpdatesActions(t)
}

func assertWorkflowCoreSecurity(t *testing.T) {
	t.Helper()

	for _, workflowPath := range workflowPaths() {
		workflow := readWorkflow(t, workflowPath)
		if _, unsafe := workflow.On["pull_request_target"]; unsafe {
			t.Errorf("%s must not use pull_request_target", workflowPath)
		}
		assertImmutableActionReferences(t, workflowPath, workflow)
	}
}

func assertCIWorkflowContract(t *testing.T) {
	t.Helper()

	workflow := readWorkflow(t, ciWorkflowPath)
	if workflow.Permissions["contents"] != "read" || len(workflow.Permissions) != 1 {
		t.Errorf("CI permissions = %v, want only contents: read", workflow.Permissions)
	}
	for _, event := range []string{"pull_request", pushEventName, "workflow_call"} {
		if _, found := workflow.On[event]; !found {
			t.Errorf("CI has no %s trigger", event)
		}
	}
	_, base := gitRevisions(t, repositoryRoot(t))
	for job, recipe := range map[string]string{
		"quality": "ci", securityName: securityName, "race": "test-race", mutationDiffJobID: mutationDiffName,
	} {
		commands := workflowCommands(t, repositoryRoot(t), workflow.Jobs[job], map[string]string{
			eventBaseEnv: base, eventNameEnv: pushEventName,
		})
		want := []string{miseExecCommand, "--", justCommand, recipe}
		if recipe == mutationDiffName {
			want = append(want, base)
		}
		if !slices.ContainsFunc(commands, func(command []string) bool { return slices.Equal(command, want) }) {
			t.Errorf("CI job %s invoked %q, want %q", job, commands, want)
		}
	}
	if workflow.Jobs["real-infrastructure"].Env["TF_MUT_ALLOW_REAL_INFRASTRUCTURE"] != "1" {
		t.Error("real infrastructure job lacks the explicit opt-in")
	}
}

func assertWorkflowMisePins(t *testing.T) {
	t.Helper()

	miseVersion := miseMinimumVersion(t, readRepositoryFile(t, "mise.toml"))
	for _, workflowPath := range workflowPaths() {
		workflow := readWorkflow(t, workflowPath)
		found := false
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				action, _, _ := strings.Cut(step.Uses, "@")
				if action == "jdx/mise-action" {
					found = true
					if step.With["version"] != miseVersion {
						t.Errorf("%s pins mise %q, want %s", workflowPath, step.With["version"], miseVersion)
					}
				}
			}
		}
		if !found {
			t.Errorf("%s has no mise setup", workflowPath)
		}
	}
}

func assertDependabotUpdatesActions(t *testing.T) {
	t.Helper()

	var dependabot struct {
		Updates []dependabotUpdate `yaml:"updates"`
	}
	if err := yaml.Unmarshal([]byte(readRepositoryFile(t, ".github/dependabot.yml")), &dependabot); err != nil {
		t.Fatalf("parse Dependabot configuration: %v", err)
	}
	for _, update := range dependabot.Updates {
		if update.Ecosystem == "github-actions" {
			return
		}
	}
	t.Error("Dependabot does not update immutable GitHub Action references")
}

func TestLockedToolsHaveDownloadIntegrity(t *testing.T) {
	t.Parallel()

	lock := readRepositoryFile(t, "mise.lock")
	toolCount := strings.Count(lock, "[[tools.")
	urlCount := strings.Count(lock, `url = "https://`)
	checksumCount := strings.Count(lock, `checksum = "sha256:`)
	if toolCount < minimumToolCount || urlCount < toolCount || checksumCount != toolCount {
		t.Fatalf(
			"mise.lock tools=%d URLs=%d checksums=%d; every tool needs a URL and checksum",
			toolCount,
			urlCount,
			checksumCount,
		)
	}
}

func assertImmutableActionReferences(t *testing.T, path string, workflow workflowContract) {
	t.Helper()

	immutableReference := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, job := range workflow.Jobs {
		uses := make([]string, 1, 1+len(job.Steps))
		uses[0] = job.Uses
		for _, step := range job.Steps {
			uses = append(uses, step.Uses)
		}
		for _, use := range uses {
			if use == "" || strings.HasPrefix(use, "./") {
				continue
			}
			_, ref, _ := strings.Cut(use, "@")
			if !immutableReference.MatchString(ref) {
				t.Errorf("%s has mutable action reference %q", path, use)
			}
		}
	}
}

func workflowCommands(t *testing.T, directory string, job workflowJob, overrides map[string]string) [][]string {
	t.Helper()

	var commands [][]string
	for _, step := range job.Steps {
		if step.Run == "" {
			continue
		}
		command := exec.CommandContext(context.Background(), "bash", "-euo", "pipefail")
		command.Dir = directory
		command.Stdin = strings.NewReader("mise() { printf '%s\\0' \"$@\"; printf '\\n'; }\n" + step.Run)
		command.Env = os.Environ()
		for _, environment := range []map[string]string{job.Env, step.Env, overrides} {
			for key, value := range environment {
				command.Env = append(command.Env, key+"="+value)
			}
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("execute workflow step: %v\n%s", err, output)
		}
		for line := range strings.SplitSeq(strings.TrimSuffix(string(output), "\n"), "\n") {
			if line != "" {
				commands = append(commands, strings.Split(strings.TrimSuffix(line, "\x00"), "\x00"))
			}
		}
	}

	return commands
}

func gitRevisions(t *testing.T, directory string) (head, parent string) {
	t.Helper()

	command := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD", "HEAD^")
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("resolve HEAD and its parent: %v", err)
	}
	revisions := strings.Fields(string(output))
	if len(revisions) != 2 {
		t.Fatalf("git returned revisions %q, want HEAD and its parent", revisions)
	}

	return revisions[0], revisions[1]
}

func TestMutationDiffResolvesEventBaseIndependentlyOfEventName(t *testing.T) {
	t.Parallel()

	job := readWorkflow(t, ciWorkflowPath).Jobs[mutationDiffJobID]
	head, parent := gitRevisions(t, repositoryRoot(t))
	for _, event := range []string{pushEventName, "pull_request", "workflow_call"} {
		for _, test := range []struct {
			name string
			base string
			want string
		}{
			{"valid", head, head},
			{"zero", strings.Repeat("0", 40), parent},
			{"empty", "", parent},
			{"non-hex", strings.Repeat("g", 40), parent},
			{"uppercase", strings.ToUpper(head), parent},
			{"short", head[:39], parent},
		} {
			t.Run(event+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				commands := workflowCommands(t, repositoryRoot(t), job, map[string]string{
					eventBaseEnv: test.base, eventNameEnv: event,
				})
				want := []string{miseExecCommand, "--", justCommand, mutationDiffName, test.want}
				if len(commands) == 0 || !slices.Equal(commands[len(commands)-1], want) {
					t.Fatalf("commands = %q, want final invocation %q", commands, want)
				}
			})
		}
	}
}

func TestMutationDiffFallbackWorksWithTestJobCheckoutDepth(t *testing.T) {
	t.Parallel()

	workflow := readWorkflow(t, ciWorkflowPath)
	for _, name := range []string{"quality", "race"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			depth := "1"
			found := false
			for _, step := range workflow.Jobs[name].Steps {
				action, _, _ := strings.Cut(step.Uses, "@")
				if action == "actions/checkout" {
					found = true
					if configured := step.With["fetch-depth"]; configured != "" {
						depth = configured
					}
				}
			}
			if !found {
				t.Fatal("test job has no checkout")
			}
			directory := shallowCheckout(t, depth)
			_, parent := gitRevisions(t, directory)
			commands := workflowCommands(t, directory, workflow.Jobs[mutationDiffJobID], map[string]string{
				eventBaseEnv: strings.Repeat("0", 40), eventNameEnv: pushEventName,
			})
			want := []string{miseExecCommand, "--", justCommand, mutationDiffName, parent}
			if len(commands) == 0 || !slices.Equal(commands[len(commands)-1], want) {
				t.Fatalf("commands = %q, want final invocation %q", commands, want)
			}
		})
	}
}

func shallowCheckout(t *testing.T, depth string) string {
	t.Helper()

	directory := t.TempDir()
	command := exec.CommandContext(context.Background(), "bash", "-euo", "pipefail")
	command.Dir = directory
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"), "CHECKOUT_DEPTH=" + depth,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
	}
	command.Stdin = strings.NewReader(`git init -q --initial-branch=main --object-format=sha1 source
for message in root parent tagged; do
  git -C source -c user.name=Test -c user.email=test@example.invalid \
    -c commit.gpgsign=false commit -q --allow-empty -m "$message"
done
git clone -q --depth "$CHECKOUT_DEPTH" "file://$PWD/source" checkout
git -C checkout rev-parse --is-shallow-repository
`)
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		t.Fatalf("create shallow checkout: %v\n%s", err, output)
	}

	return filepath.Join(directory, "checkout")
}

func miseMinimumVersion(t *testing.T, contents string) string {
	t.Helper()

	var configuration struct {
		MinimumVersion string `toml:"min_version"`
	}
	if _, err := toml.Decode(contents, &configuration); err != nil {
		t.Fatalf("parse mise configuration: %v", err)
	}

	return configuration.MinimumVersion
}

func TestMiseMinimumVersionUsesActiveTOML(t *testing.T) {
	t.Parallel()

	for _, contents := range []string{
		"# min_version = \"2026.1.0\"\nmin_version = \"2026.10.7\"\n",
		"min_version = '2026.10.7'\n",
		"\"min_version\" = \"2026.10.7\"\n",
	} {
		t.Run(contents, func(t *testing.T) {
			t.Parallel()

			if got := miseMinimumVersion(t, contents); got != "2026.10.7" {
				t.Fatalf("active min_version = %q, want 2026.10.7", got)
			}
		})
	}
}

func TestLanguageManifestsAreComplete(t *testing.T) {
	t.Parallel()

	repository := repositoryFS(t)
	repositoryPaths := repositoryFiles(t)
	wantShell := discoverFiles(t, repository, repositoryPaths, func(path string) bool {
		return hasBashShebang(t, repository, path)
	})
	wantJSON := withoutSkipped(t, "tools/json-files-skip",
		discoverExtension(t, repository, repositoryPaths, ".json"))
	wantYAML := append(
		discoverExtension(t, repository, repositoryPaths, ".yaml"),
		discoverExtension(t, repository, repositoryPaths, ".yml")...,
	)
	wantTOML := append(discoverExtension(t, repository, repositoryPaths, ".toml"), "mise.lock")

	assertManifest(t, "tools/shell-files", wantShell)
	assertManifest(t, "tools/json-files", wantJSON)
	assertManifest(t, "tools/yaml-files", wantYAML)
	assertManifest(t, "tools/toml-files", wantTOML)
}

// withoutSkipped removes the intentionally malformed fixtures a skip manifest
// names, mirroring the Terraform format skip file: a fixture that must not
// parse cannot be asked to. Each named fixture must exist and must indeed fail
// to decode — a skip entry that parses cleanly is hiding a file from the lint
// gate for no reason.
func withoutSkipped(t *testing.T, skipManifest string, discovered []string) []string {
	t.Helper()

	root := repositoryRoot(t)
	skipped := map[string]bool{}

	for rawLine := range strings.SplitSeq(readRepositoryFile(t, skipManifest), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		skipped[line] = true

		content, err := os.ReadFile(filepath.Join(root, line)) //nolint:gosec // a repository-owned path.
		if err != nil {
			t.Fatalf("%s names %s, which does not exist: %v", skipManifest, line, err)
		}

		decoded := any(nil)
		if json.Unmarshal(content, &decoded) == nil {
			t.Fatalf("%s names %s, but it parses cleanly and belongs in tools/json-files",
				skipManifest, line)
		}
	}

	kept := make([]string, 0, len(discovered))

	for _, path := range discovered {
		if !skipped[path] {
			kept = append(kept, path)
		}
	}

	return kept
}

func assertManifest(t *testing.T, path string, want []string) {
	t.Helper()

	slices.Sort(want)
	got := strings.Fields(readRepositoryFile(t, path))
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

func hasBashShebang(t *testing.T, repository fs.FS, path string) bool {
	t.Helper()

	contents, err := fs.ReadFile(repository, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	firstLine, _, _ := strings.Cut(string(contents), "\n")

	return firstLine == "#!/usr/bin/env bash"
}

func discoverExtension(
	t *testing.T,
	repository fs.FS,
	repositoryPaths []string,
	extension string,
) []string {
	t.Helper()

	return discoverFiles(t, repository, repositoryPaths, func(path string) bool {
		return filepath.Ext(path) == extension
	})
}

func discoverFiles(
	t *testing.T,
	repository fs.FS,
	repositoryPaths []string,
	include func(path string) bool,
) []string {
	t.Helper()

	var paths []string
	for _, path := range repositoryPaths {
		info, err := fs.Stat(repository, path)
		if err != nil {
			t.Fatalf("stat repository path %s: %v", path, err)
		}
		if info.Mode().IsRegular() && include(path) {
			paths = append(paths, path)
		}
	}

	return paths
}

func repositoryFiles(t *testing.T) []string {
	t.Helper()

	command := exec.CommandContext( //nolint:gosec // Static git invocation over the current repository.
		context.Background(),
		"git",
		"-C",
		repositoryRoot(t),
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
		"-z",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list repository files: %v", err)
	}
	trimmed := strings.TrimSuffix(string(output), "\x00")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "\x00")
}

func readRepositoryFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := fs.ReadFile(repositoryFS(t), path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(contents)
}

func repositoryFS(t *testing.T) fs.FS {
	t.Helper()

	return os.DirFS(repositoryRoot(t))
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	return root
}
