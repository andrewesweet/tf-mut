package buildchain_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNextPatchVersion(t *testing.T) {
	t.Parallel()

	binary := filepath.Join(t.TempDir(), "buildchain")
	build := exec.CommandContext(context.Background(), "go", "build", "-mod=readonly", "-o", binary, "./internal/buildchain/cmd")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release command: %v\n%s", err, output)
	}
	for _, test := range []struct {
		name       string
		history    []string
		args       []string
		published  string
		lookupExit string
		want       string
		code       int
		lookup     bool
	}{
		{name: "empty repository refuses", code: 1},
		{name: "repository without tags refuses", history: []string{"commit"}, code: 1},
		{name: "unpublished tag at HEAD is reused", history: []string{"commit", "v0.1.0", "commit", "v0.1.1"}, want: "v0.1.1", lookup: true},
		{name: "published tag at HEAD skips", history: []string{"commit", "v0.1.0", "commit", "v0.1.1"}, published: "v0.1.1", code: 2, lookup: true},
		{name: "publication lookup fails closed", history: []string{"commit", "v0.1.0"}, lookupExit: "1", code: 1, lookup: true},
		{name: "rerun reuses its tag after a newer release", history: []string{"commit", "v0.1.0", "commit", "v0.1.1", "commit", "v0.1.2", "checkout:v0.1.1"}, published: "v0.1.2", want: "v0.1.1", lookup: true},
		{name: "rerun skips its published tag after a newer release", history: []string{"commit", "v0.1.0", "commit", "v0.1.1", "commit", "v0.1.2", "checkout:v0.1.1"}, published: "v0.1.1\nv0.1.2", code: 2, lookup: true},
		{name: "commit past latest release increments patch", history: []string{"commit", "v0.1.0", "commit"}, want: "v0.1.1"},
		{name: "double digit versions sort numerically", history: []string{"commit", "v0.1.9", "v0.1.10", "commit"}, want: "v0.1.11"},
		{name: "non semantic tags are ignored", history: []string{"commit", "v0.1.0", "v2", "v0.2.0-rc1", "commit"}, want: "v0.1.1"},
		{name: "major and minor components carry", history: []string{"commit", "v1.2.3", "commit"}, want: "v1.2.4"},
		{name: "major component sorts numerically", history: []string{"commit", "v9.9.9", "v10.0.0", "commit"}, want: "v10.0.1"},
		{name: "minor component sorts numerically", history: []string{"commit", "v1.9.9", "v1.10.0", "commit"}, want: "v1.10.1"},
		{name: "zero patch increments", history: []string{"commit", "v2.0.0", "commit"}, want: "v2.0.1"},
		{name: "annotated predecessor increments", history: []string{"commit", "annotated:v0.1.0", "commit"}, want: "v0.1.1"},
		{name: "annotated unpublished tag is reused", history: []string{"commit", "v0.1.0", "commit", "annotated:v0.1.1"}, want: "v0.1.1", lookup: true},
		{name: "annotated published tag skips", history: []string{"commit", "v0.1.0", "commit", "annotated:v0.1.1"}, published: "v0.1.1", code: 2, lookup: true},
		{name: "successor verification", history: []string{"commit", "v0.1.0", "commit", "v0.1.1"}, args: []string{"--prev", "v0.1.1"}, want: "v0.1.1"},
		{name: "skipped minor version does not verify", history: []string{"commit", "v0.1.0", "commit", "v0.1.1"}, args: []string{"--prev", "v0.2.0"}, want: "v0.1.2"},
		{name: "future patch does not verify", history: []string{"commit", "v0.1.0", "commit", "v0.1.1"}, args: []string{"--prev", "v0.1.9"}, want: "v0.1.2"},
		{name: "first release has no predecessor", history: []string{"commit", "v0.1.0"}, args: []string{"--prev", "v0.1.0"}, code: 1},
		{name: "double digit predecessor verification", history: []string{"commit", "v0.1.9", "commit", "v0.1.10", "commit"}, args: []string{"--prev", "v0.1.11"}, want: "v0.1.11"},
		{name: "partial version refuses", args: []string{"--prev", "v0.1"}, code: 1},
		{name: "invalid version refuses", args: []string{"--prev", "banana"}, code: 1},
		{name: "no predecessor refuses", args: []string{"--prev", "v0.1.0"}, code: 1},
		{name: "missing previous version is usage error", args: []string{"--prev"}, code: 2},
		{name: "unknown mode is usage error", args: []string{"--next"}, code: 2},
		{name: "extra argument is usage error", args: []string{"--prev", "v0.1.0", "extra"}, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := releaseRepository(t, test.history)
			mockBin, calls := releaseGHBoundary(t)
			command := exec.CommandContext(context.Background(), binary, append([]string{"next-patch-version"}, test.args...)...)
			command.Dir = root
			command.Env = append(os.Environ(), "PATH="+mockBin+":"+os.Getenv("PATH"),
				"GH_TEST_CALL="+calls, "GH_TEST_PUBLISHED="+test.published, "GH_TEST_EXIT="+test.lookupExit)
			output, err := command.Output()
			code := 0
			if err != nil {
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != test.code || strings.TrimSpace(string(output)) != test.want {
				t.Fatalf("exit=%d stdout=%q, want exit=%d stdout=%q; error=%v", code, output, test.code, test.want, err)
			}
			invocation, readErr := os.ReadFile(calls)
			if !test.lookup {
				if !os.IsNotExist(readErr) {
					t.Fatalf("unexpected GitHub lookup: %s (%v)", invocation, readErr)
				}
				return
			}
			wantArgs := []string{"api", "--paginate", "repos/{owner}/{repo}/releases", "--jq", ".[] | select(.draft == false) | .tag_name"}
			if readErr != nil || !slices.Equal(strings.Split(strings.TrimSuffix(string(invocation), "\n"), "\n"), wantArgs) {
				t.Fatalf("publication lookup = %q (%v), want %q", invocation, readErr, wantArgs)
			}
		})
	}
}

func releaseRepository(t *testing.T, history []string) string {
	t.Helper()

	root := t.TempDir()
	releaseGit(t, root, "init", "-q", "--initial-branch=master")
	for _, action := range history {
		switch {
		case action == "commit":
			releaseGit(t, root, "commit", "-q", "--allow-empty", "-m", "change")
		case strings.HasPrefix(action, "annotated:"):
			tag := strings.TrimPrefix(action, "annotated:")
			releaseGit(t, root, "tag", "-a", tag, "-m", tag)
		case strings.HasPrefix(action, "checkout:"):
			releaseGit(t, root, "checkout", "-q", strings.TrimPrefix(action, "checkout:"))
		default:
			releaseGit(t, root, "tag", action)
		}
	}
	return root
}

func releaseGit(t *testing.T, root string, args ...string) string {
	t.Helper()

	options := []string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}
	command := exec.CommandContext(context.Background(), "git", append(options, args...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func releaseGHBoundary(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	calls := filepath.Join(root, "calls")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GH_TEST_CALL\"\nprintf '%s\\n' \"${GH_TEST_PUBLISHED-}\"\nexit \"${GH_TEST_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(root, "gh"), []byte(stub), testDirectoryMode); err != nil {
		t.Fatal(err)
	}
	return root, calls
}

func TestReleaseTagCreationReusesExistingTag(t *testing.T) {
	t.Parallel()

	steps := readWorkflow(t, ".github/workflows/release-on-merge.yml").Jobs["tag"].Steps
	for _, existing := range []bool{false, true} {
		name := "new tag"
		if existing {
			name = "existing tag"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			history := []string{"commit", "v0.1.0", "commit"}
			if existing {
				history = append(history, "v0.1.1")
			}
			root := releaseRepository(t, history)
			head := releaseGit(t, root, "rev-parse", "HEAD")
			mockBin, calls := releaseGHBoundary(t)
			command := exec.CommandContext(context.Background(), "bash", "-euo", "pipefail")
			command.Dir = root
			command.Stdin = strings.NewReader(steps[len(steps)-1].Run)
			command.Env = append(os.Environ(), "PATH="+mockBin+":"+os.Getenv("PATH"), "GH_TEST_CALL="+calls,
				"GITHUB_REPOSITORY=example/test", "VERSION=v0.1.1", "GITHUB_SHA="+head)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create or reuse tag: %v\n%s", err, output)
			}
			invocation, err := os.ReadFile(calls)
			if existing {
				if !os.IsNotExist(err) {
					t.Fatalf("reused tag caused an API mutation: %q (%v)", invocation, err)
				}
				if target := releaseGit(t, root, "rev-parse", "v0.1.1^{commit}"); target != head {
					t.Fatalf("tag moved to %s, want %s", target, head)
				}
				return
			}
			want := []string{"api", "--method", "POST", "repos/example/test/git/refs", "-f", "ref=refs/tags/v0.1.1", "-f", "sha=" + head}
			if err != nil || !slices.Equal(strings.Split(strings.TrimSuffix(string(invocation), "\n"), "\n"), want) {
				t.Fatalf("tag creation = %q (%v), want %q", invocation, err, want)
			}
		})
	}
}

func TestReleaseWrapperPreservesPublishedSkip(t *testing.T) {
	t.Parallel()

	root := releaseRepository(t, []string{"commit", "v0.1.0", "commit", "v0.1.1"})
	mockBin, calls := releaseGHBoundary(t)
	command := exec.CommandContext(context.Background(), filepath.Join(repositoryRoot(t), "scripts/next-patch-version"))
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+mockBin+":"+os.Getenv("PATH"),
		"GH_TEST_CALL="+calls, "GH_TEST_PUBLISHED=v0.1.1")
	output, err := command.Output()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 || len(output) != 0 {
		t.Fatalf("wrapper exit=%v stdout=%q, want exit=2 and empty stdout", err, output)
	}
}
