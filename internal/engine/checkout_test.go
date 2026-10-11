package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
)

// The checkout boundary: a run block whose local module source resolves
// outside the git work tree is refused with a message that names the test
// file, the run block and the source string — not with the raw read error
// of the directory the discovery went on to open.

func TestARunBlockSourceLeavingTheCheckoutIsRefusedWithTheRunNamed(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "runblock-outside")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if err == nil {
		t.Fatal("a run block whose source leaves the checkout must be refused")
	}

	for _, fragment := range []string{
		"leaves the checkout",
		unitTestFile,
		`run "outside"`,
		`"../outside-sibling"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}
}

func TestARunBlockSourceMissingInsideTheCheckoutIsRefusedPrecisely(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "runblock-outside")
	rewriteRunSource(t, module, `"../outside-sibling"`, `"./no-such-sibling"`,
		`run "outside"`, `run "missing"`)

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if err == nil {
		t.Fatal("a run block whose source resolves to nothing must be refused")
	}

	for _, fragment := range []string{
		"no such directory in the checkout",
		unitTestFile,
		`run "missing"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("refusal does not name %s: %v", fragment, err)
		}
	}

	if strings.Contains(err.Error(), "reading ") {
		t.Fatalf("the refusal leaks the raw read error: %v", err)
	}
}

// rewriteRunSource replaces the test files' source and run name, turning the
// escaping retarget into one that resolves inside the checkout to a directory
// that is not there.
func rewriteRunSource(t *testing.T, module string, replacements ...string) {
	t.Helper()

	if len(replacements)%2 != 0 {
		t.Fatal("replacements must come in from/to pairs")
	}

	entries, err := os.ReadDir(filepath.Join(module, "tests"))
	if err != nil {
		t.Fatalf("reading the test directory: %v", err)
	}

	for _, entry := range entries {
		path := filepath.Join(module, "tests", entry.Name())

		content, readErr := os.ReadFile(path) //nolint:gosec // the path is a test-owned fixture copy.
		if readErr != nil {
			t.Fatalf("reading %s: %v", path, readErr)
		}

		rewritten := string(content)

		for index := 0; index+1 < len(replacements); index += 2 {
			rewritten = strings.Replace(rewritten,
				replacements[index], replacements[index+1], 1)
		}

		//nolint:gosec // the path is a test-owned fixture copy.
		if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}
