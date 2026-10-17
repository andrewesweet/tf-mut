package tfexec_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// A fake Terraform child: it spawns a long-lived grandchild in its own
// process group — the shape of a provider plugin Terraform leaves behind —
// and then exits normally, the way the report's leaked providers outlived a
// healthy run rather than a cancelled one.
const leakyChild = `#!/bin/sh
# Provider plugins never hold Terraform's own stdout pipe open — the report's
# leaked runs all completed with verdicts — so the grandchild closes the
# stdio it inherits. What it keeps is its process-group membership: that is
# the orphan mechanism under test.
sleep 300 <&- >&- 2>&- &
echo $! > "$GRANDCHILD_PID_FILE"
exit 0
`

// After a normal exit, no process the command started may outlive it. Without
// the post-exit group kill, the sleep grandchild above keeps running: it is
// the report's orphaned terraform-provider-* mechanism in miniature.
func TestNormalExitReapsTheProcessGroup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	child := filepath.Join(dir, "fake-terraform.sh")

	if err := os.WriteFile(child, []byte(leakyChild), 0o700); err != nil {
		t.Fatalf("writing the fake child: %v", err)
	}

	pidFile := filepath.Join(dir, "grandchild.pid")

	runner := tfexec.Runner{Binary: child}
	runner.Env = []string{"GRANDCHILD_PID_FILE=" + pidFile}

	result, err := runner.Run(context.Background(), dir)
	if err != nil {
		t.Fatalf("running the fake child: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0: the child must exit normally", result.ExitCode)
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading the grandchild pid: %v", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parsing the grandchild pid: %v", err)
	}

	// The kill is sent before Run returns, but the kernel reaps
	// asynchronously; poll briefly rather than asserting instant death.
	deadline := time.Now().Add(5 * time.Second)

	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}

		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)

			t.Fatalf("grandchild pid %d still running after the command exited normally", pid)
		}

		time.Sleep(50 * time.Millisecond)
	}
}
