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

const debugCLIChild = "TF_MUT_DEBUG_CLI_CHILD"

func TestCacheDebugLogsAreReachableThroughTheCLI(t *testing.T) {
	t.Parallel()

	for _, command := range []string{runCommand, suggestCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			module := fixture(t)
			for _, enabled := range []bool{false, true} {
				var stdout, stderr bytes.Buffer
				//nolint:gosec // Re-execute this test binary through the standard helper-process pattern.
				child := exec.CommandContext(t.Context(), os.Args[0],
					"-test.run=^TestDebugLoggingCLIProcess$", "--",
					command, reporterFlag, reporterJSON, "--jobs", "1", module)
				const secret = "cache-debug-cli-secret"
				child.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
					return strings.HasPrefix(entry, "TF_MUT_DEBUG=")
				})
				child.Env = append(child.Env, debugCLIChild+"=1",
					"TF_MUT_DEBUG_PROBE="+secret, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1")
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

				decoded := decodeReport(t, stdout.Bytes())
				if len(decoded.Mutants) == 0 {
					t.Fatal("the CLI returned no mutation population")
				}

				logs := stderr.String()
				if strings.Contains(logs, secret) {
					t.Fatal("an environment value reached stderr")
				}

				if !enabled {
					if strings.Contains(logs, "level=DEBUG") {
						t.Fatalf("debug logging enabled by default: %s", logs)
					}
					continue
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
			}
		})
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
