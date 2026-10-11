package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNeutraliseBackendsPreservesFilesWithoutABackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{
		"array.tf.json":      `[{"terraform":{"required_version":">= 1.6"}}]`,
		"null.tf.json":       "{\n  \"terraform\": null\n}\n",
		"null-array.tf.json": `{"terraform":[null,{"required_version":">= 1.6"},null]}`,
		"main.tf":            "resource   \"terraform_data\" \"r\" {\n  input = \"kept\"\n}\n",
		"providers.tf.json": "{\n  \"terraform\": {\n    \"required_providers\": {\n" +
			"      \"null\": {\n        \"source\": \"hashicorp/null\"\n      }\n" +
			"    }\n  }\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	rewrites, err := neutraliseBackends(dir, t.TempDir())
	if err != nil {
		t.Fatalf("neutraliseBackends: %v", err)
	}

	if len(rewrites) != 0 {
		t.Fatalf("files without a backend produced %d rewrites", len(rewrites))
	}

	for name, content := range files {
		got, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the test's own temporary directory.
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}

		if !bytes.Equal(got, []byte(content)) {
			t.Fatalf("%s was rewritten:\n%s\nwant:\n%s", name, got, content)
		}
	}
}

func TestNeutraliseBackendsRefusesAMalformedStagedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("terraform {\n  backend\n"), 0o600); err != nil {
		t.Fatalf("writing main.tf: %v", err)
	}

	if _, err := neutraliseBackends(dir, t.TempDir()); err == nil {
		t.Fatal("malformed staged source was accepted")
	}
}
