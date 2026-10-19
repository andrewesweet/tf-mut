package engine_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

func TestNormalExitReapsTheProcessGroup(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)
	if runtime.GOOS != "linux" {
		t.Skip("provider liveness reads Linux /proc process state")
	}

	for _, subcommand := range []string{"providers", testSubcommand} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			pidFile := filepath.Join(dir, "providers.pid")
			testsupport.WriteFile(t, pidFile, "")
			t.Cleanup(func() {
				for _, pid := range recordedProviderPIDs(t, pidFile) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})

			wrapper := filepath.Join(dir, "terraform-leaking-provider")
			testsupport.WriteExecutable(t, wrapper, `#!/usr/bin/env bash
if [ "$2" != "$TF_MUT_REAP_COMMAND" ]; then
  exec terraform "$@"
fi
terraform "$@"
status=$?
provider_dir="${1#-chdir=}/.terraform/providers/registry.terraform.io/hashicorp/null/3.2.4/linux_amd64"
provider=("$provider_dir/"terraform-provider-null*)
log="$TF_MUT_PROVIDER_PIDS.$$.log"
"${provider[0]}" -debug < /dev/null > "$log" 2>&1 &
pid=$!
printf '%s\n' "$pid" >> "$TF_MUT_PROVIDER_PIDS"
for ((attempt = 0; attempt < 500; attempt++)); do
  if grep -q TF_REATTACH_PROVIDERS "$log"; then
    exit "$status"
  fi
  if ! kill -0 "$pid" 2>/dev/null; then
    break
  fi
  sleep 0.01
done
cat "$log" >&2
exit 1
`)

			module := copyFixture(t, "mocked-null")
			before := treeDigest(t, module)
			request := baseConfig(t, module)
			request.TerraformBinary = wrapper
			request.IncludeOperators = []string{string(mutation.StrCase)}
			request.Env = append(request.Env,
				"TF_MUT_REAP_COMMAND="+subcommand,
				"TF_MUT_PROVIDER_PIDS="+pidFile,
			)

			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(result.Errors) != 0 || result.Count(report.Killed) == 0 || result.Count(report.Timeout) != 0 {
				t.Fatalf("expected normally completed mutation tests, got counts %v, errors %v",
					result.Metrics.Counts, result.Errors)
			}

			pids := recordedProviderPIDs(t, pidFile)
			if len(pids) == 0 {
				t.Fatalf("no provider was started by terraform %s", subcommand)
			}

			assertProvidersStopped(t, pids, subcommand)

			assertTreeUnchanged(t, module, before)
		})
	}
}

func assertProvidersStopped(t *testing.T, pids []int, subcommand string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for _, pid := range pids {
		for providerIsRunning(t, pid) {
			if time.Now().After(deadline) {
				t.Fatalf("terraform-provider-null pid %d outlived terraform %s", pid, subcommand)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func recordedProviderPIDs(t *testing.T, path string) []int {
	t.Helper()

	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading provider pids: %v", err)
	}

	fields := strings.Fields(string(content))
	pids := make([]int, 0, len(fields))
	for _, field := range fields {
		pid, parseErr := strconv.Atoi(field)
		if parseErr != nil || pid <= 0 {
			t.Fatalf("invalid provider pid %q", field)
		}
		pids = append(pids, pid)
	}

	return pids
}

func providerIsRunning(t *testing.T, pid int) bool {
	t.Helper()

	status, err := os.ReadFile(filepath.Clean(fmt.Sprintf("/proc/%d/stat", pid)))
	if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
		return false
	}
	if err != nil {
		t.Fatalf("reading provider pid %d state: %v", pid, err)
	}

	end := bytes.LastIndexByte(status, ')')
	if end < 0 || len(status) <= end+2 {
		t.Fatalf("invalid process state for provider pid %d: %q", pid, status)
	}

	state := status[end+2]

	return state != 'Z' && state != 'X'
}
