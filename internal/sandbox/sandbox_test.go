package sandbox_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// These are the only tests in this repository that do not go through the engine
// seam. They exist because the milestone spec asks for the inode assertion
// itself to fail loudly (issue #12), and an assertion that can only be observed
// through a whole mutation run is an assertion nobody can trust. Everything
// about mutation *behaviour* is still tested at the seam.

func TestWritingOverTheSourceFileIsRefused(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "main.tf")
	original := []byte("output \"kept\" {\n  value = \"original\"\n}\n")

	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	err := sandbox.WriteFresh(source, source, []byte("mutated"))
	if !errors.Is(err, sandbox.ErrSharedInode) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrSharedInode)
	}

	assertContent(t, source, original)
}

func TestWritingThroughAHardlinkToTheSourceIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "main.tf")
	target := filepath.Join(root, "sandbox.tf")
	original := []byte("output \"kept\" {\n  value = \"original\"\n}\n")

	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	// The R2-3 trap: a sandbox file linked from the source shares its
	// inode, so writing it in place truncates the original checkout. The
	// contract is that the writer refuses before writing, not that it papers
	// over the situation.
	if err := os.Link(source, target); err != nil {
		t.Fatalf("hardlinking: %v", err)
	}

	err := sandbox.WriteFresh(target, source, []byte("mutated"))
	if !errors.Is(err, sandbox.ErrSharedInode) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrSharedInode)
	}

	assertContent(t, source, original)
	assertContent(t, target, original)
}

func TestWritingAFreshFileSucceedsAndDoesNotShareTheSourceInode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "main.tf")
	target := filepath.Join(root, "sandbox", "main.tf")
	original := []byte("output \"kept\" {\n  value = \"original\"\n}\n")

	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	if err := sandbox.WriteFresh(target, source, []byte("mutated")); err != nil {
		t.Fatalf("writing mutated file: %v", err)
	}

	assertContent(t, source, original)
	assertContent(t, target, []byte("mutated"))

	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatalf("inspecting source: %v", err)
	}

	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("inspecting target: %v", err)
	}

	if os.SameFile(sourceInfo, targetInfo) {
		t.Fatal("the mutated file shares the source inode")
	}
}

func assertContent(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path) //nolint:gosec // test-owned temporary path.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	if string(got) != string(want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
