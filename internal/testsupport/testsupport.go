// Package testsupport holds the file-writing helper shared by the engine
// and CLI test suites, so the file-creation mode has exactly one definition.
package testsupport

import (
	"os"
	"testing"
)

// fixtureMode gives newly created fixture files owner read and write access only.
const fixtureMode = 0o600

// WriteFile writes content to path, failing the test on error. New files use
// fixtureMode; existing files keep their permissions. Every caller passes a
// path under t.TempDir().
func WriteFile(tb testing.TB, path, content string) {
	tb.Helper()

	if err := os.WriteFile(path, []byte(content), fixtureMode); err != nil {
		tb.Fatalf("writing %s: %v", path, err)
	}
}
