package buildchain_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/buildchain"
)

const (
	testProviderAddress  = "registry.terraform.io/hashicorp/null"
	testProviderVersion  = "3.2.4"
	testProviderPlatform = "linux_amd64"
	testProviderArchive  = ".tools/providers/registry.terraform.io/hashicorp/null/" +
		"terraform-provider-null_3.2.4_linux_amd64.zip"
	testFileMode      = 0o600
	testDirectoryMode = 0o750
)

func TestVerifyProviderChainAcceptsConsistentHermeticFixture(t *testing.T) {
	t.Parallel()

	root := writeProviderChainFixture(t)
	err := buildchain.VerifyProviderChain(root)
	if err != nil {
		t.Fatalf("verify provider chain: %v", err)
	}
}

func TestVerifyProviderChainRejectsTamperedArchive(t *testing.T) {
	t.Parallel()

	root := writeProviderChainFixture(t)
	err := os.WriteFile(filepath.Join(root, testProviderArchive), []byte("tampered"), testFileMode)
	if err != nil {
		t.Fatalf("tamper with provider archive: %v", err)
	}
	err = buildchain.VerifyProviderChain(root)
	if err == nil {
		t.Fatal("provider chain accepted an archive whose checksum is absent from the lock")
	}
}

func TestGoToolchainVersions(t *testing.T) {
	t.Parallel()

	versions, err := buildchain.GoToolchainVersions("go1.26.6")
	if err != nil {
		t.Fatalf("derive Go versions: %v", err)
	}
	if versions.Language != "1.26.0" {
		t.Fatalf("language version = %q, want 1.26.0", versions.Language)
	}
	if versions.Exact != "go1.26.6" {
		t.Fatalf("toolchain version = %q, want go1.26.6", versions.Exact)
	}
}

func TestGoToolchainVersionsRejectsNonStableVersion(t *testing.T) {
	t.Parallel()

	_, err := buildchain.GoToolchainVersions("go1.27rc1")
	if err == nil {
		t.Fatal("non-stable Go version was accepted")
	}
}

func writeProviderChainFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	archive := []byte("hermetic provider archive fixture")
	archiveHash := sha256.Sum256(archive)
	files := map[string][]byte{
		"tools/providers.allowlist": fmt.Appendf(nil,
			"%s %s %s\n",
			testProviderAddress,
			testProviderVersion,
			testProviderPlatform,
		),
		"research/spikes/fixture-b/main.tf": fmt.Appendf(nil,
			"source = %q\nversion = %q\n",
			"hashicorp/null",
			testProviderVersion,
		),
		"research/spikes/fixture-b/.terraform.lock.hcl": fmt.Appendf(nil,
			"provider %q {\n  version     = %q\n  constraints = %q\n  hashes = [%q]\n}\n",
			testProviderAddress,
			testProviderVersion,
			testProviderVersion,
			fmt.Sprintf("zh:%x", archiveHash),
		),
		testProviderArchive: archive,
		".tools/terraform-cli.tfrc": fmt.Appendf(nil,
			"%s\n%s\n%s\n",
			filepath.Join(root, ".tools/providers"),
			filepath.Join(root, ".tools/terraform-plugin-cache"),
			testProviderAddress,
		),
	}
	for name, contents := range files {
		writeFixtureFile(t, root, name, contents)
	}

	return root
}

func writeFixtureFile(t *testing.T, root, name string, contents []byte) {
	t.Helper()

	path := filepath.Join(root, name)
	err := os.MkdirAll(filepath.Dir(path), testDirectoryMode)
	if err != nil {
		t.Fatalf("create fixture directory for %s: %v", name, err)
	}
	err = os.WriteFile(path, contents, testFileMode)
	if err != nil {
		t.Fatalf("write fixture file %s: %v", name, err)
	}
}
