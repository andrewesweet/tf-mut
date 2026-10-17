package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

const (
	debugCLIChild = "TF_MUT_DEBUG_CLI_CHILD"
	debugCLIProbe = "cache-debug-cli-probe"
)

func TestCacheDebugLogsAreReachableThroughTheCLI(t *testing.T) {
	t.Parallel()

	for _, command := range []string{runCommand, suggestCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			module := fixture(t)
			for invocation, enabled := range []bool{false, true, true} {
				decoded, logs := runCacheDebugCLI(t, command, module, enabled)
				if len(decoded.Mutants) == 0 {
					t.Fatal("the CLI returned no mutation population")
				}
				if invocation == 2 && (decoded.Population.Cached == 0 || decoded.Population.Fresh != 0) {
					t.Fatalf("repeated debug invocation did not replay the cache: %+v", decoded.Population)
				}

				assertCacheDebugLogs(t, decoded, logs, enabled)
			}
		})
	}
}

func runCacheDebugCLI(t *testing.T, command, module string, enabled bool) (report.Report, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	//nolint:gosec // Re-execute this test binary through the standard helper-process pattern.
	child := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=^TestDebugLoggingCLIProcess$", "--",
		command, reporterFlag, reporterJSON, "--jobs", "1", module)
	child.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "TF_MUT_DEBUG=")
	})
	child.Env = append(child.Env, debugCLIChild+"=1",
		"TF_MUT_DEBUG_PROBE="+debugCLIProbe, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
	if enabled {
		child.Env = append(child.Env, "TF_MUT_DEBUG=1")
	}

	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		exit := &exec.ExitError{}
		if !errors.As(err, &exit) || exit.ExitCode() != report.ExitFindings {
			t.Fatalf("CLI failed: %v\n%s", err, stderr.String())
		}
	}

	return decodeReport(t, stdout.Bytes()), stderr.String()
}

func assertCacheDebugLogs(t *testing.T, decoded report.Report, logs string, enabled bool) {
	t.Helper()

	if strings.Contains(logs, debugCLIProbe) {
		t.Fatal("an environment value reached stderr")
	}
	if decoded.Telemetry == nil || decoded.Telemetry.RunID == "" {
		t.Fatal("the report has no run identifier")
	}
	prefix := "[" + decoded.Telemetry.RunID + "] "
	for line := range strings.SplitSeq(strings.TrimSuffix(logs, "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, prefix) {
			t.Fatalf("stderr line lacks the report's run identifier %q: %q", prefix, line)
		}
	}

	if !enabled {
		if strings.Contains(logs, "level=DEBUG") {
			t.Fatalf("debug logging enabled by default: %s", logs)
		}
		return
	}

	for _, component := range []string{
		"format", "terraform", "platform", "baseline", "config", "source", "test", "env",
	} {
		pattern := `level=DEBUG msg="tf-mut: cache key component" component=` + component + ` digest=([0-9a-f]+)`
		match := regexp.MustCompile(pattern).FindStringSubmatch(logs)
		if len(match) != 2 {
			t.Fatalf("missing debug digest for %s: %s", component, logs)
		}
		if digest, err := hex.DecodeString(match[1]); err != nil || len(digest) != 32 {
			t.Fatalf("invalid component digest: %q", match[1])
		}
	}
	if !regexp.MustCompile(`(?m)level=DEBUG msg="tf-mut: cache key" key=[0-9a-f]{64}$`).MatchString(logs) {
		t.Fatalf("missing full cache key: %s", logs)
	}
}

func TestDebugLoggingCLIProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv(debugCLIChild) != "1" {
		t.Skip("helper process only")
	}

	for index, arg := range os.Args {
		if arg == "--" {
			os.Args = append(os.Args[:1], os.Args[index+1:]...)
			main()
			return
		}
	}

	t.Fatal("missing CLI arguments")
}
