package buildchain_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/buildchain"
)

func TestProviderChainIsConsistent(t *testing.T) {
	t.Parallel()

	err := buildchain.VerifyProviderChain(repositoryRoot(t))
	if err != nil {
		t.Fatalf("verify provider chain: %v", err)
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
