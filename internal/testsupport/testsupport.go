// Package testsupport holds the file-writing helper shared by the engine
// and CLI test suites, so the fixture-copy mode has exactly one definition.
package testsupport

import (
	"os"
	"testing"
)

// fixtureMode is the permission every test fixture copy gets: owner read and
// write only, matching what the suites wrote before the helper was shared.
const fixtureMode = 0o600

// WriteFile writes content to path with the fixture-copy mode, failing the
// test on error. Every caller passes a path under t.TempDir().
func WriteFile(tb testing.TB, path, content string) {
	tb.Helper()

	if err := os.WriteFile(path, []byte(content), fixtureMode); err != nil {
		tb.Fatalf("writing %s: %v", path, err)
	}
}
