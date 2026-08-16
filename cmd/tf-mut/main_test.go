package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type testReporter interface {
	Helper()
	Fatalf(format string, args ...any)
}

const emptyOutput = ""

func TestVersionAliases(t *testing.T) {
	t.Parallel()

	for _, command := range []string{versionCommand, versionFlag} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			assertVersion(t, command, "1.2.3")
		})
	}
}

func TestVersionOutputPreservesBuildValue(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		command := rapid.SampledFrom([]string{versionCommand, versionFlag}).
			Draw(t, "command")
		buildVersion := rapid.StringMatching(
			`[a-zA-Z0-9][a-zA-Z0-9._+-]{0,31}`,
		).Draw(t, "build version")
		assertVersion(t, command, buildVersion)
	})
}

func TestEmptyBuildVersionReportsDevelopmentVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{versionCommand}, "", &stdout, &stderr)

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d", exitCode, exitSuccess)
	}
	if got, want := stdout.String(), "dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func assertVersion(t testReporter, command, buildVersion string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{command}, buildVersion, &stdout, &stderr)

	if exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d", exitCode, exitSuccess)
	}
	if got, want := stdout.String(), buildVersion+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.String() != emptyOutput {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func FuzzInvalidCommand(f *testing.F) {
	for _, command := range []string{"", "help", "Version", "status"} {
		f.Add(command)
	}

	f.Fuzz(func(t *testing.T, command string) {
		if command == versionCommand || command == versionFlag ||
			command == runCommand || command == previewCommand ||
			strings.ContainsRune(command, '\x00') {
			t.Skip()
		}

		assertInvalidCommand(t, command)
	})
}

func assertInvalidCommand(t testReporter, command string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{command}, "dev", &stdout, &stderr)

	if exitCode != exitUsage {
		t.Fatalf(
			"exit code = %d, want %d for %s",
			exitCode,
			exitUsage,
			fmt.Sprintf("%q", command),
		)
	}
	if stdout.String() != emptyOutput {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got != usage+"\n" {
		t.Fatalf("stderr = %q, want usage", got)
	}
}
