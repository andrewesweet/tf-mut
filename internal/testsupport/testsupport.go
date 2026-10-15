// Package testsupport holds the file-writing helpers shared by the engine
// and CLI test suites, so each file-creation mode has exactly one definition.
package testsupport

import (
	"os"
	"syscall"
	"testing"
)

// fixtureMode gives newly created fixture files owner read and write access only.
const fixtureMode = 0o600

// executableMode gives test scripts owner read, write and execute access only.
const executableMode = 0o700

// WriteFile writes content to path, failing the test on error. New files use
// fixtureMode; existing files keep their permissions. Every caller passes a
// path under t.TempDir().
func WriteFile(tb testing.TB, path, content string) {
	tb.Helper()

	if err := os.WriteFile(path, []byte(content), fixtureMode); err != nil {
		tb.Fatalf("writing %s: %v", path, err)
	}
}

// WriteExecutable writes a test-owned script under t.TempDir(), excluding
// concurrent forks until its writable descriptor is closed. Close-on-exec
// alone lets a forked child hold the descriptor until exec, causing ETXTBSY
// when another child tries to execute the script.
func WriteExecutable(tb testing.TB, path, content string) {
	tb.Helper()

	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	if err := os.WriteFile(path, []byte(content), executableMode); err != nil {
		tb.Fatalf("writing %s: %v", path, err)
	}
}
