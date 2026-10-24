package buildchain_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	releaseFirstPatch             = "v0.1.1"
	releaseSecondPatch            = "v0.1.2"
	releaseNinthPatch             = "v0.1.9"
	releaseCommit                 = "commit"
	releaseInitialTag             = "v0.1.0"
	releaseCheckoutFirstPatch     = "checkout:v0.1.1"
	releaseCheckoutPreviousCommit = "checkout:HEAD~1"
	releasePreviousFlag           = "--prev"
	releaseAnnotatedFirstPatch    = "annotated:v0.1.1"
	releaseDoubleDigitSuccessor   = "v0.1.11"
)

type releaseVersionCase struct {
	name       string
	history    []string
	args       []string
	published  string
	lookupExit string
	want       string
	code       int
	lookup     bool
}

func TestNextPatchVersion(t *testing.T) {
	t.Parallel()

	binary := filepath.Join(t.TempDir(), "buildchain")
	//nolint:gosec // Fixed Go package built into a test-owned temporary directory.
	build := exec.CommandContext(context.Background(), "go", "build", "-mod=readonly", "-o", binary,
		"./internal/buildchain/cmd")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release command: %v\n%s", err, output)
	}
	for _, test := range slices.Concat(
		releaseSelectionCases(), releaseAncestryCases(), releaseOrderingCases(), releaseVerificationCases(),
	) {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := releaseRepository(t, test.history)
			mockBin, calls := releaseGHBoundary(t)
			//nolint:gosec // Runs the command built above with arguments from the static case table.
			command := exec.CommandContext(context.Background(), binary, append([]string{"next-patch-version"}, test.args...)...)
			command.Dir = root
			command.Env = append(fixtureGitEnvironment(), "PATH="+mockBin+":"+os.Getenv("PATH"),
				"GH_TEST_CALL="+calls, "GH_TEST_PUBLISHED="+test.published, "GH_TEST_EXIT="+test.lookupExit)
			output, err := command.Output()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				ok := errors.As(err, &exit)
				if !ok {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != test.code || strings.TrimSpace(string(output)) != test.want {
				t.Fatalf("exit=%d stdout=%q, want exit=%d stdout=%q; error=%v", code, output, test.code, test.want, err)
			}
			//nolint:gosec // The path is the call log created by the test's GitHub boundary.
			invocation, readErr := os.ReadFile(calls)
			if !test.lookup {
				if !os.IsNotExist(readErr) {
					t.Fatalf("unexpected GitHub lookup: %s (%v)", invocation, readErr)
				}
				return
			}
			wantArgs := []string{
				"api", "--paginate", "repos/{owner}/{repo}/releases", "--jq", ".[] | select(.draft == false) | .tag_name",
			}
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
		case action == releaseCommit || strings.HasPrefix(action, "commit:"):
			releaseGit(t, root, releaseCommit, "-q", "--allow-empty", "-m", action)
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

	options := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
	}, args...)
	//nolint:gosec // Git commands and arguments come from the static release fixture cases.
	command := exec.CommandContext(context.Background(), "git", options...)
	command.Dir = root
	command.Env = fixtureGitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// fixtureGitEnvironment keeps tool settings while preventing a caller's Git
// locations or configuration from redirecting commands outside the fixture.
func fixtureGitEnvironment() []string {
	environment := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}
	return environment
}

func releaseGHBoundary(t *testing.T) (root, calls string) {
	t.Helper()

	root = t.TempDir()
	calls = filepath.Join(root, "calls")
	stub := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$GH_TEST_CALL\"\n" +
		"printf '%s\\n' \"${GH_TEST_PUBLISHED-}\"\n" +
		"exit \"${GH_TEST_EXIT:-0}\"\n"
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

			history := []string{releaseCommit, releaseInitialTag, releaseCommit}
			if existing {
				history = append(history, releaseFirstPatch)
			}
			root := releaseRepository(t, history)
			head := releaseGit(t, root, "rev-parse", "HEAD")
			mockBin, calls := releaseGHBoundary(t)
			command := exec.CommandContext(context.Background(), "bash", "-euo", "pipefail")
			command.Dir = root
			command.Stdin = strings.NewReader(steps[len(steps)-1].Run)
			command.Env = append(fixtureGitEnvironment(), "PATH="+mockBin+":"+os.Getenv("PATH"), "GH_TEST_CALL="+calls,
				"GITHUB_REPOSITORY=example/test", "VERSION=v0.1.1", "GITHUB_SHA="+head)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create or reuse tag: %v\n%s", err, output)
			}
			//nolint:gosec // The path is the call log created by the test's GitHub boundary.
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
			want := []string{
				"api", "--method", "POST", "repos/example/test/git/refs", "-f", "ref=refs/tags/v0.1.1", "-f", "sha=" + head,
			}
			if err != nil || !slices.Equal(strings.Split(strings.TrimSuffix(string(invocation), "\n"), "\n"), want) {
				t.Fatalf("tag creation = %q (%v), want %q", invocation, err, want)
			}
		})
	}
}

func TestReleaseWrapperPreservesPublishedSkip(t *testing.T) {
	t.Parallel()

	root := releaseRepository(t, []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch})
	mockBin, calls := releaseGHBoundary(t)
	//nolint:gosec // Runs the repository-owned release wrapper at its fixed relative path.
	command := exec.CommandContext(context.Background(), filepath.Join(repositoryRoot(t), "scripts/next-patch-version"))
	command.Dir = root
	command.Env = append(fixtureGitEnvironment(), "PATH="+mockBin+":"+os.Getenv("PATH"),
		"GH_TEST_CALL="+calls, "GH_TEST_PUBLISHED=v0.1.1")
	output, err := command.Output()
	var exit *exec.ExitError
	ok := errors.As(err, &exit)
	if !ok || exit.ExitCode() != 2 || len(output) != 0 {
		t.Fatalf("wrapper exit=%v stdout=%q, want exit=2 and empty stdout", err, output)
	}
}

func releaseSelectionCases() []releaseVersionCase {
	return []releaseVersionCase{
		{
			name: "empty repository refuses",
			code: 1,
		},
		{
			name:    "repository without tags refuses",
			history: []string{releaseCommit},
			code:    1,
		},
		{
			name:    "unpublished tag at HEAD is reused",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch},
			want:    releaseFirstPatch,
			lookup:  true,
		},
		{
			name:      "published tag at HEAD skips",
			history:   []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch},
			published: releaseFirstPatch,
			code:      2,
			lookup:    true,
		},
		{
			name:       "publication lookup fails closed",
			history:    []string{releaseCommit, releaseInitialTag},
			lookupExit: "1",
			code:       1,
			lookup:     true,
		},
		{
			name: "rerun reuses its tag after a newer release",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch, releaseCommit, releaseSecondPatch,
				releaseCheckoutFirstPatch,
			},
			published: releaseSecondPatch,
			want:      releaseFirstPatch,
			lookup:    true,
		},
		{
			name: "rerun skips its published tag after a newer release",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch, releaseCommit, releaseSecondPatch,
				releaseCheckoutFirstPatch,
			},
			published: "v0.1.1\nv0.1.2",
			code:      2,
			lookup:    true,
		},
	}
}

func releaseAncestryCases() []releaseVersionCase {
	return []releaseVersionCase{
		{
			name: "untagged stale rerun refuses descendant predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseCommit, releaseFirstPatch,
				releaseCheckoutPreviousCommit,
			},
			code: 1,
		},
		{
			name: "stale successor verification refuses descendant predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseCommit, releaseFirstPatch,
				releaseCheckoutPreviousCommit,
			},
			args: []string{releasePreviousFlag, releaseSecondPatch},
			code: 1,
		},
		{
			name: "untagged stale rerun refuses annotated descendant predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseCommit, releaseAnnotatedFirstPatch,
				releaseCheckoutPreviousCommit,
			},
			code: 1,
		},
		{
			name: "stale successor verification refuses annotated descendant predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseCommit, releaseAnnotatedFirstPatch,
				releaseCheckoutPreviousCommit,
			},
			args: []string{releasePreviousFlag, releaseSecondPatch},
			code: 1,
		},
		{
			name: "fresh successor refuses unrelated predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch, "checkout:v0.1.0", "commit:side",
			},
			code: 1,
		},
		{
			name: "successor verification refuses unrelated predecessor",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch, "checkout:v0.1.0", "commit:side",
			},
			args: []string{releasePreviousFlag, releaseSecondPatch},
			code: 1,
		},
		{
			name:    "predecessor at HEAD verifies",
			history: []string{releaseCommit, releaseInitialTag},
			args:    []string{releasePreviousFlag, releaseFirstPatch},
			want:    releaseFirstPatch,
		},
		{
			name: "existing tagged rerun verifies after newer release",
			history: []string{
				releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch, releaseCommit, releaseSecondPatch,
				releaseCheckoutFirstPatch,
			},
			args: []string{releasePreviousFlag, releaseFirstPatch},
			want: releaseFirstPatch,
		},
	}
}

func releaseOrderingCases() []releaseVersionCase {
	return []releaseVersionCase{
		{
			name:    "commit past latest release increments patch",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit},
			want:    releaseFirstPatch,
		},
		{
			name:    "double digit versions sort numerically",
			history: []string{releaseCommit, releaseNinthPatch, "v0.1.10", releaseCommit},
			want:    releaseDoubleDigitSuccessor,
		},
		{
			name:    "non semantic tags are ignored",
			history: []string{releaseCommit, releaseInitialTag, "v2", "v0.2.0-rc1", releaseCommit},
			want:    releaseFirstPatch,
		},
		{
			name:    "major and minor components carry",
			history: []string{releaseCommit, "v1.2.3", releaseCommit},
			want:    "v1.2.4",
		},
		{
			name:    "major component sorts numerically",
			history: []string{releaseCommit, "v9.9.9", "v10.0.0", releaseCommit},
			want:    "v10.0.1",
		},
		{
			name:    "minor component sorts numerically",
			history: []string{releaseCommit, "v1.9.9", "v1.10.0", releaseCommit},
			want:    "v1.10.1",
		},
		{
			name:    "zero patch increments",
			history: []string{releaseCommit, "v2.0.0", releaseCommit},
			want:    "v2.0.1",
		},
		{
			name:    "annotated predecessor increments",
			history: []string{releaseCommit, "annotated:v0.1.0", releaseCommit},
			want:    releaseFirstPatch,
		},
		{
			name:    "annotated unpublished tag is reused",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit, releaseAnnotatedFirstPatch},
			want:    releaseFirstPatch,
			lookup:  true,
		},
		{
			name:      "annotated published tag skips",
			history:   []string{releaseCommit, releaseInitialTag, releaseCommit, releaseAnnotatedFirstPatch},
			published: releaseFirstPatch,
			code:      2,
			lookup:    true,
		},
		{
			name:    "successor verification",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch},
			args:    []string{releasePreviousFlag, releaseFirstPatch},
			want:    releaseFirstPatch,
		},
		{
			name:    "skipped minor version does not verify",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch},
			args:    []string{releasePreviousFlag, "v0.2.0"},
			want:    releaseSecondPatch,
		},
	}
}

func releaseVerificationCases() []releaseVersionCase {
	return []releaseVersionCase{
		{
			name:    "future patch does not verify",
			history: []string{releaseCommit, releaseInitialTag, releaseCommit, releaseFirstPatch},
			args:    []string{releasePreviousFlag, releaseNinthPatch},
			want:    releaseSecondPatch,
		},
		{
			name:    "first release has no predecessor",
			history: []string{releaseCommit, releaseInitialTag},
			args:    []string{releasePreviousFlag, releaseInitialTag},
			code:    1,
		},
		{
			name:    "double digit predecessor verification",
			history: []string{releaseCommit, releaseNinthPatch, releaseCommit, "v0.1.10", releaseCommit},
			args:    []string{releasePreviousFlag, releaseDoubleDigitSuccessor},
			want:    releaseDoubleDigitSuccessor,
		},
		{
			name: "partial version refuses",
			args: []string{releasePreviousFlag, "v0.1"},
			code: 1,
		},
		{
			name: "invalid version refuses",
			args: []string{releasePreviousFlag, "banana"},
			code: 1,
		},
		{
			name: "no predecessor refuses",
			args: []string{releasePreviousFlag, releaseInitialTag},
			code: 1,
		},
		{
			name: "missing previous version is usage error",
			args: []string{releasePreviousFlag},
			code: 2,
		},
		{
			name: "unknown mode is usage error",
			args: []string{"--next"},
			code: 2,
		},
		{
			name: "extra argument is usage error",
			args: []string{releasePreviousFlag, releaseInitialTag, "extra"},
			code: 2,
		},
	}
}
