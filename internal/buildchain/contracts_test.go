package buildchain_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	ciWorkflowPath     = ".github/workflows/ci.yml"
	codeQLWorkflowPath = ".github/workflows/codeql.yml"
	minimumToolCount   = 1
)

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
		"test-integration", "test-performance", "fuzz", "fuzz-all", "mutate-diff",
		"mutate", "security", "agent-check", "ci", "ci-full",
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

	workflow := readRepositoryFile(t, ".github/workflows/codeql.yml")
	for _, required := range []string{
		"language: go",
		"language: actions",
		"security-and-quality",
		"security-events: write",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CodeQL workflow does not contain %q", required)
		}
	}
	if strings.Contains(workflow, "pull_request_target") {
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

	for _, workflowPath := range []string{ciWorkflowPath, codeQLWorkflowPath} {
		workflow := readRepositoryFile(t, workflowPath)
		if strings.Contains(workflow, "pull_request_target") {
			t.Errorf("%s must not use pull_request_target", workflowPath)
		}
		assertImmutableActionReferences(t, workflowPath, workflow)
	}
}

func assertCIWorkflowContract(t *testing.T) {
	t.Helper()

	ciWorkflow := readRepositoryFile(t, ciWorkflowPath)
	for _, required := range []string{
		"permissions:\n  contents: read",
		"mise exec -- just ci",
		"mise exec -- just security",
		"mise exec -- just test-race",
		"mise exec -- just mutate-diff",
		`TF_MUT_ALLOW_REAL_INFRASTRUCTURE: "1"`,
	} {
		if !strings.Contains(ciWorkflow, required) {
			t.Errorf("CI workflow does not contain %q", required)
		}
	}
}

func assertWorkflowMisePins(t *testing.T) {
	t.Helper()

	miseVersion := valueBetween(t, readRepositoryFile(t, "mise.toml"), `min_version = "`, `"`)
	for _, workflowPath := range []string{ciWorkflowPath, codeQLWorkflowPath} {
		if !strings.Contains(readRepositoryFile(t, workflowPath), "version: "+miseVersion) {
			t.Errorf("%s does not pin mise %s", workflowPath, miseVersion)
		}
	}
}

func assertDependabotUpdatesActions(t *testing.T) {
	t.Helper()

	dependabot := readRepositoryFile(t, ".github/dependabot.yml")
	if !strings.Contains(dependabot, `package-ecosystem: "github-actions"`) {
		t.Error("Dependabot does not update immutable GitHub Action references")
	}
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

func assertImmutableActionReferences(t *testing.T, path, workflow string) {
	t.Helper()

	usesLine := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*[^\s@]+@([^\s]+)`)
	immutableReference := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, match := range usesLine.FindAllStringSubmatch(workflow, -1) {
		if !immutableReference.MatchString(match[1]) {
			t.Errorf("%s has mutable action reference %q", path, match[1])
		}
	}
}

func valueBetween(t *testing.T, contents, prefix, suffix string) string {
	t.Helper()

	_, after, found := strings.Cut(contents, prefix)
	if !found {
		t.Fatalf("content does not contain %q", prefix)
	}
	value, _, found := strings.Cut(after, suffix)
	if !found || value == "" {
		t.Fatalf("content does not terminate %q with %q", prefix, suffix)
	}

	return value
}

func TestLanguageManifestsAreComplete(t *testing.T) {
	t.Parallel()

	repository := repositoryFS(t)
	repositoryPaths := repositoryFiles(t)
	wantShell := discoverFiles(t, repository, repositoryPaths, func(path string) bool {
		return hasBashShebang(t, repository, path)
	})
	wantJSON := discoverExtension(t, repository, repositoryPaths, ".json")
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
