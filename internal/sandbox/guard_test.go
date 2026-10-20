package sandbox_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// The guard contract: the lock a mutant sandbox receives records no hashes, so
// the checksum Terraform would have run at execution time is skipped by
// design. What keeps that honest is the warm-up stamp — every provider binary
// reachable from the warm tree gets its size and modification time recorded
// once, and any sandbox that shares the tree is refused when one of them
// changed. One stat per recorded provider per sandbox.

// writeWarmProviderTree builds a warm .terraform/providers tree in the shape
// the warm-up init leaves behind and returns the recorded binary's path.
func writeWarmProviderTree(t *testing.T, warmDataDir string) string {
	t.Helper()

	packageDir := filepath.Join(
		warmDataDir, "providers", "registry.terraform.io", "hashicorp", "null", "3.2.3",
	)

	if err := os.MkdirAll(packageDir, 0o750); err != nil {
		t.Fatalf("creating the provider package directory: %v", err)
	}

	binary := filepath.Join(packageDir, "terraform-provider-null")
	if err := os.WriteFile(binary, []byte("provider binary"), 0o600); err != nil {
		t.Fatalf("writing the provider binary: %v", err)
	}

	return binary
}

// materialiseGuarded builds a guard over the warm tree and materialises one
// sandbox that shares it.
func materialiseGuarded(t *testing.T, warmDataDir string, guard *sandbox.ProviderGuard) error {
	t.Helper()

	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatalf("creating the source: %v", err)
	}

	target := filepath.Join(t.TempDir(), "sandbox")

	_, err := sandbox.Materialise(sandbox.Spec{
		SourceRoot: source,
		ModuleRel:  ".",
		Target:     target,
		Share: &sandbox.Share{
			DataDir: warmDataDir,
			Guard:   guard,
		},
	})

	return err
}

func TestAnUnchangedProviderTreeMaterialises(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	writeWarmProviderTree(t, warmDataDir)

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	if err := materialiseGuarded(t, warmDataDir, guard); err != nil {
		t.Fatalf("an unchanged tree was refused: %v", err)
	}
}

func TestAGrownProviderBinaryRefusesTheSandbox(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	binary := writeWarmProviderTree(t, warmDataDir)

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	// A concurrent init swapped the cache entry: same file, more bytes.
	rewriteBinary(t, binary, []byte("...replacement bytes"))

	err = materialiseGuarded(t, warmDataDir, guard)

	if !errors.Is(err, sandbox.ErrProviderChanged) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrProviderChanged)
	}
}

func TestATouchedProviderBinaryRefusesTheSandbox(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	binary := writeWarmProviderTree(t, warmDataDir)

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	// Same bytes, same size, a later modification time.
	touched := time.Unix(1234567890, 0)
	if err := os.Chtimes(binary, touched, touched); err != nil {
		t.Fatalf("touching the provider binary: %v", err)
	}

	if err := materialiseGuarded(t, warmDataDir, guard); !errors.Is(err, sandbox.ErrProviderChanged) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrProviderChanged)
	}
}

func TestARemovedProviderBinaryRefusesTheSandbox(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	binary := writeWarmProviderTree(t, warmDataDir)

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	if err := os.Remove(binary); err != nil {
		t.Fatalf("removing the provider binary: %v", err)
	}

	if err := materialiseGuarded(t, warmDataDir, guard); !errors.Is(err, sandbox.ErrProviderChanged) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrProviderChanged)
	}
}

func TestARefusedSandboxLeavesNoDirectoryBehind(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	binary := writeWarmProviderTree(t, warmDataDir)

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	rewriteBinary(t, binary, []byte("!"))

	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatalf("creating the source: %v", err)
	}

	target := filepath.Join(t.TempDir(), "sandbox")

	_, _ = sandbox.Materialise(sandbox.Spec{
		SourceRoot: source,
		ModuleRel:  ".",
		Target:     target,
		Share: &sandbox.Share{
			DataDir: warmDataDir,
			Guard:   guard,
		},
	})

	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the refused sandbox left a directory behind")
	}
}

func TestTheGuardStampsThroughThePluginCacheSymlink(t *testing.T) {
	t.Parallel()

	// The user-level plugin cache layout: the warm tree reaches each package
	// through a symlink, so the binary that executes lives outside the tree
	// the guard is handed. The stamp must follow the link; a change at the
	// end of it is a change to the binary that would execute.
	cacheDir := filepath.Join(t.TempDir(), "plugin-cache", "registry.terraform.io", "hashicorp", "null", "3.2.3")
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		t.Fatalf("creating the plugin cache package: %v", err)
	}

	binary := filepath.Join(cacheDir, "terraform-provider-null")
	if err := os.WriteFile(binary, []byte("provider binary"), 0o600); err != nil {
		t.Fatalf("writing the provider binary: %v", err)
	}

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	link := filepath.Join(
		warmDataDir, "providers", "registry.terraform.io", "hashicorp", "null", "3.2.3",
	)

	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatalf("creating the provider tree: %v", err)
	}

	if err := os.Symlink(cacheDir, link); err != nil {
		t.Fatalf("linking the package into the warm tree: %v", err)
	}

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping the warm tree: %v", err)
	}

	rewriteBinary(t, binary, []byte("swapped"))

	if err := materialiseGuarded(t, warmDataDir, guard); !errors.Is(err, sandbox.ErrProviderChanged) {
		t.Fatalf("error = %v, want %v", err, sandbox.ErrProviderChanged)
	}
}

func TestAWorkspaceWithoutProvidersNeedsNoGuard(t *testing.T) {
	t.Parallel()

	warmDataDir := filepath.Join(t.TempDir(), "warm", "module", sandbox.DataDirName)
	if err := os.MkdirAll(warmDataDir, 0o750); err != nil {
		t.Fatalf("creating the warm data directory: %v", err)
	}

	guard, err := sandbox.StampProviders(warmDataDir)
	if err != nil {
		t.Fatalf("stamping a providerless workspace: %v", err)
	}

	if err := materialiseGuarded(t, warmDataDir, guard); err != nil {
		t.Fatalf("a providerless workspace was refused: %v", err)
	}
}

// rewriteBinary swaps the recorded binary's bytes, as a concurrent init
// swapping a plugin-cache entry would.
func rewriteBinary(t *testing.T, path string, extra []byte) {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // test-owned temporary path.
	if err != nil {
		t.Fatalf("reading the provider binary: %v", err)
	}

	writeErr := os.WriteFile(path, append(content, extra...), 0o600) //nolint:gosec // test-owned temporary path.
	if writeErr != nil {
		t.Fatalf("rewriting the provider binary: %v", writeErr)
	}
}
