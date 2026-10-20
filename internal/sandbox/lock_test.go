package sandbox_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// These are document contracts under the testing seam's recorded exception 9:
// the shape of the lock file a sandbox receives, and the byte identity of the
// locks the source tree and the warm workspace keep. Every verdict staging
// leads to is still asserted through the engine seam; the engine cannot be
// driven into showing these bytes.

const lockWithHashes = `# This file is maintained automatically by "terraform init".
# Manual edits may be lost in future updates.

provider "registry.terraform.io/hashicorp/null" {
  version     = "3.2.3"
  constraints = ">= 3.0.0"
  hashes = [
    "h1:F8GrmCteOhs/2n4XuxZ9vG3MvVbamDyCv7spJcwN1rM=",
    "zh:0e6c2088a031a535c1bbd28b217e5e2c2d3f0f0e5f4d3c2b1a0987654321abcd",
  ]
}

provider "registry.terraform.io/hashicorp/time" {
  version     = "0.12.1"
  constraints = "0.12.1"
  hashes = [
    "h1:Cv1AlcLZkW+fUWGGCPPSgbppGGm1PSEZqRRwcPjfZkQ=",
    "zh:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
  ]
}
`

// lockFixture is the shape every provider-bearing run has: a source tree with
// a lock, and a warm workspace materialised from it, so the warm lock is the
// source lock's copy.
type lockFixture struct {
	sourceRoot  string
	sourceLock  string
	warmDataDir string
	warmLock    string
}

func writeLockFixture(t *testing.T, lock string) lockFixture {
	t.Helper()

	fixture := lockFixture{
		sourceRoot:  filepath.Join(t.TempDir(), "source"),
		warmDataDir: filepath.Join(filepath.Join(t.TempDir(), "warm", "module"), sandbox.DataDirName),
	}

	for _, dir := range []string{fixture.sourceRoot, fixture.warmDataDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	module := []byte("output \"kept\" {\n  value = true\n}\n")
	if err := os.WriteFile(filepath.Join(fixture.sourceRoot, "main.tf"), module, 0o600); err != nil {
		t.Fatalf("writing the module: %v", err)
	}

	fixture.sourceLock = filepath.Join(fixture.sourceRoot, sandbox.LockFileName)
	fixture.warmLock = filepath.Join(
		filepath.Dir(fixture.warmDataDir), sandbox.LockFileName,
	)

	for _, path := range []string{fixture.sourceLock, fixture.warmLock} {
		if err := os.WriteFile(path, []byte(lock), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	return fixture
}

// stageSandbox materialises one sandbox over the lock fixture the way the
// engine does, and returns where the sandbox's own lock landed.
func stageSandbox(t *testing.T, sourceRoot, warmDataDir, warmLockFile string) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), "sandbox")

	_, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: sourceRoot,
		ModuleRel:  ".",
		Target:     target,
		Share: &sandbox.Share{
			DataDir:  warmDataDir,
			LockFile: warmLockFile,
		},
	})
	if err != nil {
		t.Fatalf("materialising: %v", err)
	}

	return filepath.Join(target, sandbox.LockFileName)
}

func TestTheStagedLockIsTheWarmLockWithoutHashes(t *testing.T) {
	t.Parallel()

	fixture := writeLockFixture(t, lockWithHashes)
	staged := stageSandbox(t, fixture.sourceRoot, fixture.warmDataDir, fixture.warmLock)

	content, err := os.ReadFile(staged) //nolint:gosec // test-owned temporary path.
	if err != nil {
		t.Fatalf("reading the staged lock: %v", err)
	}

	file, diags := hclwrite.ParseConfig(content, staged, hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("the staged lock does not parse: %s", diags.Error())
	}

	blocks := file.Body().Blocks()
	if len(blocks) != 2 {
		t.Fatalf("the staged lock has %d provider blocks, want 2", len(blocks))
	}

	for _, block := range blocks {
		name := block.Labels()[0]

		if block.Body().GetAttribute("hashes") != nil {
			t.Errorf("%s still records hashes", name)
		}

		if block.Body().GetAttribute("version") == nil {
			t.Errorf("%s lost its version", name)
		}

		if block.Body().GetAttribute("constraints") == nil {
			t.Errorf("%s lost its constraints", name)
		}
	}

	if !strings.Contains(string(content), "3.2.3") || !strings.Contains(string(content), "0.12.1") {
		t.Error("the staged lock lost the locked versions")
	}
}

func TestStagingKeepsTheWarmAndSourceLocksByteIdentical(t *testing.T) {
	t.Parallel()

	fixture := writeLockFixture(t, lockWithHashes)

	warmBefore, err := os.ReadFile(fixture.warmLock)
	if err != nil {
		t.Fatalf("reading the warm lock: %v", err)
	}

	sourceBefore, err := os.ReadFile(fixture.sourceLock)
	if err != nil {
		t.Fatalf("reading the source lock: %v", err)
	}

	stageSandbox(t, fixture.sourceRoot, fixture.warmDataDir, fixture.warmLock)

	warmAfter, err := os.ReadFile(fixture.warmLock)
	if err != nil {
		t.Fatalf("reading the warm lock: %v", err)
	}

	sourceAfter, err := os.ReadFile(fixture.sourceLock)
	if err != nil {
		t.Fatalf("reading the source lock: %v", err)
	}

	if string(warmBefore) != string(warmAfter) {
		t.Error("staging changed the warm lock")
	}

	if string(sourceBefore) != string(sourceAfter) {
		t.Error("staging changed the source lock")
	}
}

func TestTheStagedLockIsAFreshInode(t *testing.T) {
	t.Parallel()

	fixture := writeLockFixture(t, lockWithHashes)
	staged := stageSandbox(t, fixture.sourceRoot, fixture.warmDataDir, fixture.warmLock)

	stagedInfo, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("inspecting the staged lock: %v", err)
	}

	for _, name := range map[string]string{
		"warm":   fixture.warmLock,
		"source": fixture.sourceLock,
	} {
		other, statErr := os.Stat(name)
		if statErr != nil {
			t.Fatalf("inspecting the %s lock: %v", name, statErr)
		}

		if os.SameFile(stagedInfo, other) {
			t.Errorf("the staged lock shares the %s lock's inode", name)
		}
	}
}

func TestAMissingWarmLockLeavesTheSandboxWithoutALockFile(t *testing.T) {
	t.Parallel()

	fixture := writeLockFixture(t, lockWithHashes)

	// A module without a lock has neither a source nor a warm lock to stage;
	// the warm workspace carries none either.
	if err := os.Remove(fixture.sourceLock); err != nil {
		t.Fatalf("removing %s: %v", fixture.sourceLock, err)
	}

	staged := stageSandbox(t, fixture.sourceRoot, fixture.warmDataDir, "")

	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the sandbox gained a lock file the warm workspace never had")
	}
}

func TestAnUnparseableWarmLockRefusesTheSandbox(t *testing.T) {
	t.Parallel()

	// Fail closed: a lock hclwrite cannot parse is refused, never staged with
	// its hashes intact, because staging the hashes would silently pay the
	// checksum again and a silent fallback would hide the parse failure.
	fixture := writeLockFixture(t, "provider {\n  version =\n}\n")

	target := filepath.Join(t.TempDir(), "sandbox")

	_, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: fixture.sourceRoot,
		ModuleRel:  ".",
		Target:     target,
		Share: &sandbox.Share{
			DataDir:  fixture.warmDataDir,
			LockFile: fixture.warmLock,
		},
	})
	if err == nil {
		t.Fatal("an unparseable warm lock staged anyway")
	}

	if errors.Is(err, sandbox.ErrProviderChanged) {
		t.Errorf("error = %v, want a lock-parse refusal", err)
	}
}
