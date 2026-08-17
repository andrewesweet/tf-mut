//go:build integration

package engine_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
)

// The Terraform-identity key dimension (#48 review): a different real
// Terraform binary — same module, same everything else — must invalidate the
// cache. The second binary is a genuine adjacent release, downloaded here,
// because the fixed seam forbids a fake runner and the offline suite carries
// only one pinned binary.

// secondTerraformVersion is the adjacent release the dimension test drives.
const secondTerraformVersion = "1.15.7"

// It downloads a release archive; nothing else should share the network
// budget.
//
//nolint:paralleltest // downloads and caches a release archive serially.
func TestTerraformIdentityIsAKeyDimension(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, "all-killed")

	config := baseConfig(t, module)
	first, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	if first.Population.Cached != 0 {
		t.Fatal("the first run hit an empty cache")
	}

	warm, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("warm run: %v", err)
	}

	if warm.Population.Cached == 0 {
		t.Fatal("an unchanged run missed the cache; the dimension test is vacuous")
	}

	// The same module under a different real binary: everything else in the
	// key is identical, so only the Terraform identity can miss.
	config.TerraformBinary = secondTerraform(t)

	other, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("second-binary run: %v", err)
	}

	if other.Population.Cached != 0 {
		t.Fatalf("%d cached verdicts crossed a Terraform identity change",
			other.Population.Cached)
	}
}

// secondTerraform downloads and caches the adjacent release binary.
func secondTerraform(t *testing.T) string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		t.Skip("repository root not found")
	}

	directory := filepath.Join(root, ".artifacts", "cache", "terraform-"+secondTerraformVersion)
	binary := filepath.Join(directory, "terraform")

	if _, err := os.Stat(binary); err == nil {
		return binary
	}

	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("creating %s: %v", directory, err)
	}

	url := "https://releases.hashicorp.com/terraform/" + secondTerraformVersion +
		"/terraform_" + secondTerraformVersion + "_linux_amd64.zip"

	response, err := http.Get(url) //nolint:noctx // a fixed release URL in a gated test.
	if err != nil {
		t.Fatalf("downloading %s: %v", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("downloading %s: %s", url, response.Status)
	}

	archive, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading release archive: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("opening release archive: %v", err)
	}

	for _, file := range reader.File {
		if file.Name != "terraform" {
			continue
		}

		content, openErr := file.Open()
		if openErr != nil {
			t.Fatalf("opening binary in archive: %v", openErr)
		}

		extracted, readErr := io.ReadAll(content)
		_ = content.Close()

		if readErr != nil {
			t.Fatalf("extracting binary: %v", readErr)
		}

		//nolint:gosec // an executable needs execute permission.
		if writeErr := os.WriteFile(binary, extracted, 0o750); writeErr != nil {
			t.Fatalf("writing %s: %v", binary, writeErr)
		}

		return binary
	}

	t.Fatal("the release archive carries no terraform binary")

	return ""
}
