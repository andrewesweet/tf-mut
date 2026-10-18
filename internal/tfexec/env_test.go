//nolint:testpackage // the child environment is assembled by an unexported constructor.
package tfexec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// The child environment contract: the runner hands Terraform the caller's
// environment minus the git location variables, plus the performance defaults
// for the managed variables the caller has not set, plus the run's own
// additions last. Every verdict Terraform produces is produced under this
// environment, so the contract is asserted at the boundary that assembles it
// — the pure constructor for the parts no real Terraform run can be driven
// into proving on demand, and the concrete runner with a stub binary for the
// parts exec itself owns (duplicate resolution, the process environment's
// reach).

// managedVariables are the variables the child environment manages. The tests
// name them explicitly so that adding or renaming a default has to update the
// assertions on purpose rather than absorb them silently.
//
//nolint:gochecknoglobals // an immutable list of the names the tests assert on.
var managedVariables = []string{goGCVariable, goMemoryLimitVariable, tfLogSDKVariable}

const (
	// inheritedPath stands in for the rest of the caller's environment in the
	// pure constructor tests.
	inheritedPath = "PATH=/usr/bin"
	// additionGOGC is a run's own override of the GOGC default.
	additionGOGC = goGCVariable + "=50"
)

// unsetVariable removes one variable from the test process's environment,
// restoring it when the test ends. The end-to-end tests read the real process
// environment — inheritedEnvironment copies it — so a variable the Go
// toolchain or the developer's shell exported would make the default branch
// of an assertion unobservable.
func unsetVariable(t *testing.T, name string) {
	t.Helper()

	previous, wasSet := os.LookupEnv(name)
	if !wasSet {
		return
	}

	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unsetting %s: %v", name, err)
	}

	t.Cleanup(func() {
		t.Setenv(name, previous)
	})
}

// entriesNamed returns every environment entry carrying name, in order, so
// that the assertions can hold both the values and their count: a default
// appended behind a caller's entry would lose to it under os/exec's
// last-entry-wins resolution only by luck of ordering, and the count is what
// proves the default was never appended at all.
func entriesNamed(environment []string, name string) []string {
	entries := []string{}

	for _, entry := range environment {
		if candidate, _, found := strings.Cut(entry, "="); found && candidate == name {
			entries = append(entries, entry)
		}
	}

	return entries
}

func TestChildEnvironmentAddsTheDefaultsTheCallerHasNotSet(t *testing.T) {
	t.Parallel()

	environment := childEnvironment([]string{inheritedPath, "HOME=/home/dev"}, nil)

	want := map[string]string{
		goGCVariable:          "400",
		goMemoryLimitVariable: "1073741824",
		tfLogSDKVariable:      "off",
	}

	for name, value := range want {
		if got := entriesNamed(environment, name); !slices.Equal(got, []string{name + "=" + value}) {
			t.Fatalf("child %s entries = %q, want exactly [%s=%s]", name, got, name, value)
		}
	}

	if got := entriesNamed(environment, "PATH"); !slices.Equal(got, []string{inheritedPath}) {
		t.Fatalf("inherited PATH = %q, want it preserved", got)
	}
}

func TestChildEnvironmentHonoursAValueTheCallerAlreadySet(t *testing.T) {
	t.Parallel()

	inherited := []string{"GOGC=100", "TF_LOG_SDK=trace", "GOMEMLIMIT=64MiB"}

	environment := childEnvironment(inherited, nil)

	for _, name := range managedVariables {
		if got := entriesNamed(environment, name); len(got) != 1 {
			t.Fatalf("child %s entries = %q, want exactly the caller's single entry", name, got)
		}
	}

	if got := entriesNamed(environment, "GOGC"); !slices.Equal(got, []string{"GOGC=100"}) {
		t.Fatalf("child GOGC = %q, want the caller's [GOGC=100]", got)
	}

	if got := entriesNamed(environment, "TF_LOG_SDK"); !slices.Equal(got, []string{"TF_LOG_SDK=trace"}) {
		t.Fatalf("child TF_LOG_SDK = %q, want the caller's [TF_LOG_SDK=trace]", got)
	}

	if got := entriesNamed(environment, "GOMEMLIMIT"); !slices.Equal(got, []string{"GOMEMLIMIT=64MiB"}) {
		t.Fatalf("child GOMEMLIMIT = %q, want the caller's [GOMEMLIMIT=64MiB]", got)
	}
}

func TestChildEnvironmentAppendsTheRunAdditionsLast(t *testing.T) {
	t.Parallel()

	environment := childEnvironment([]string{inheritedPath}, []string{additionGOGC})

	got := entriesNamed(environment, goGCVariable)
	if !slices.Equal(got, []string{"GOGC=400", additionGOGC}) {
		t.Fatalf("child GOGC entries = %q, want the default then the addition in order", got)
	}

	if last := environment[len(environment)-1]; last != additionGOGC {
		t.Fatalf("last child entry = %q, want the run's addition last", last)
	}
}

// environmentDumpingRunner writes a stub that stands in for the Terraform
// binary, prints its own environment to stdout and exits successfully. The
// stub observes the environment the runner assembles; it stubs nothing the
// runner computes a verdict from.
func environmentDumpingRunner(t *testing.T) Runner {
	t.Helper()

	path := filepath.Join(t.TempDir(), "terraform-stub")
	testsupport.WriteExecutable(t, path, "#!/usr/bin/env bash\nenv\nexit 0\n")

	return Runner{Binary: path}
}

// childEnvironmentOf runs the stub once and parses its environment dump into
// a name-to-values index that preserves duplicates, because the number of
// entries a name carries is part of the contract: os/exec resolves duplicates
// in favour of the last, so a default appended behind a caller's value would
// override it by accident.
func childEnvironmentOf(t *testing.T, runner Runner) map[string][]string {
	t.Helper()

	result, err := runner.Run(t.Context(), t.TempDir(), "version")
	if err != nil {
		t.Fatalf("stub run: %v", err)
	}

	environment := map[string][]string{}

	for entry := range strings.SplitSeq(string(result.Stdout), "\n") {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}

		environment[name] = append(environment[name], value)
	}

	return environment
}

func assertSingleEntry(t *testing.T, environment map[string][]string, name, want string) {
	t.Helper()

	values := environment[name]
	if len(values) != 1 || values[0] != want {
		t.Fatalf("child %s = %q, want exactly [%s]", name, values, want)
	}
}

func TestTheRunnerHandsTerraformThePerformanceDefaults(t *testing.T) {
	for _, name := range managedVariables {
		unsetVariable(t, name)
	}

	t.Setenv("GIT_DIR", "/somewhere/else")

	before := os.Environ()

	environment := childEnvironmentOf(t, environmentDumpingRunner(t))

	assertSingleEntry(t, environment, goGCVariable, "400")
	assertSingleEntry(t, environment, goMemoryLimitVariable, "1073741824")
	assertSingleEntry(t, environment, tfLogSDKVariable, "off")

	if _, found := environment["GIT_DIR"]; found {
		t.Fatalf("child GIT_DIR = %q, want it stripped", environment["GIT_DIR"])
	}

	if after := os.Environ(); !slices.Equal(before, after) {
		t.Fatal("the runner changed the tool's own process environment")
	}
}

func TestTheRunnerKeepsTheValuesTheCallerSet(t *testing.T) {
	t.Setenv(goGCVariable, "100")
	t.Setenv(goMemoryLimitVariable, "512MiB")
	unsetVariable(t, tfLogSDKVariable)

	environment := childEnvironmentOf(t, environmentDumpingRunner(t))

	assertSingleEntry(t, environment, goGCVariable, "100")
	assertSingleEntry(t, environment, goMemoryLimitVariable, "512MiB")
	assertSingleEntry(t, environment, tfLogSDKVariable, "off")
}

func TestTheRunnerAdditionsWinOverTheDefaults(t *testing.T) {
	t.Setenv(goGCVariable, "100")

	runner := environmentDumpingRunner(t)
	runner.Env = []string{additionGOGC}

	environment := childEnvironmentOf(t, runner)

	assertSingleEntry(t, environment, goGCVariable, "50")
}
