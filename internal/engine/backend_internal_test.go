package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// File names and literals the staged roots below declare.
const (
	stagedMainFile = "main.tf"
	stagedJSONFile = "main.tf.json"

	stagedBackendName   = "azurerm"
	stagedKeptResource  = "terraform_data"
	stagedKeptProviders = "required_providers"
)

// JSON documents in both legal spellings, split only to keep the lines short.
const (
	jsonArrayBackend = `{"terraform":[{"backend":{"azurerm":{}},` +
		`"required_providers":{"null":{}}}],"resource":` +
		`{"terraform_data":{"r":{"input":"kept"}}}}`

	jsonObjectBackend = `{"terraform":{"backend":{"azurerm":{}},` +
		`"required_providers":{"null":{}}},"resource":` +
		`{"terraform_data":{"r":{"input":"kept"}}}}`

	// The shape the json-provider fixture carries, where the terraform
	// object declares no backend.
	jsonObjectNoBackend = "{\n  \"terraform\": {\n    \"required_providers\": {\n" +
		"      \"null\": {\n        \"source\": \"hashicorp/null\"\n      }\n" +
		"    }\n  }\n}\n"
)

// stripCase is one staged root: the files it declares, the content that must
// survive the strip, the content that must not, and the files that must stay
// byte-identical because nothing in them names a backend.
type stripCase struct {
	name      string
	files     map[string]string
	wantGone  []string
	wantKept  []string
	identical []string
}

// The strip is a contract about documents: which declarations survive a
// staged rewrite and which file is rewritten at all. The real binary cannot
// be driven into each of these shapes on demand, so the shapes are exercised
// directly, and every verdict they lead to — a backend-declaring root getting
// its schema and a green baseline — is asserted through the engine seam
// (TestDeclaredBackendDoesNotBlockTheSchemaOrTheSuite).
func TestNeutraliseBackends(t *testing.T) {
	t.Parallel()

	cases := []stripCase{
		{
			name: "backend removed, the rest of the terraform block kept",
			files: map[string]string{
				stagedMainFile: "terraform {\n  required_version = \">= 1.6\"\n\n  backend \"azurerm\" {}\n}\n",
			},
			wantGone: []string{backendBlock, stagedBackendName},
			wantKept: []string{"terraform", "required_version", "\">= 1.6\""},
		},
		{
			name: "blocks outside the terraform block are untouched",
			files: map[string]string{
				stagedMainFile: "terraform {\n  backend \"local\" {}\n}\n\n" +
					"resource \"terraform_data\" \"r\" {\n  input = \"kept\"\n}\n",
			},
			wantGone: []string{"backend", "\"local\" {}"},
			wantKept: []string{stagedKeptResource, "input", "kept"},
		},
		{
			name: "a JSON-declared backend is removed too",
			files: map[string]string{
				stagedJSONFile: jsonArrayBackend,
			},
			wantGone: []string{backendBlock, stagedBackendName},
			wantKept: []string{stagedKeptResource, stagedKeptProviders},
		},
		{
			// The object spelling is the one real configurations use: it is
			// the shape terraform write and the one the json-provider fixture
			// carries.
			name: "a JSON backend in the object spelling is removed too",
			files: map[string]string{
				stagedJSONFile: jsonObjectBackend,
			},
			wantGone: []string{backendBlock, stagedBackendName},
			wantKept: []string{stagedKeptResource, stagedKeptProviders},
		},
		{
			// Not canonically formatted, on purpose: a rewrite would reformat
			// it, and the strip must not change the unformatted warning.
			name: "a file without a backend is byte-identical",
			files: map[string]string{
				stagedMainFile: "resource   \"terraform_data\" \"r\" {\n  input = \"kept\"\n}\n",
			},
			identical: []string{stagedMainFile},
		},
		{
			name: "a JSON terraform object without a backend is byte-identical",
			files: map[string]string{
				"providers.tf.json": jsonObjectNoBackend,
			},
			identical: []string{"providers.tf.json"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for name, content := range testCase.files {
				writeStagedFile(t, dir, name, content)
			}

			if err := neutraliseBackends(dir); err != nil {
				t.Fatalf("neutraliseBackends: %v", err)
			}

			for _, name := range testCase.identical {
				if got := readStagedFile(t, dir, name); got != testCase.files[name] {
					t.Fatalf("%s was rewritten:\n%s\nwant:\n%s", name, got, testCase.files[name])
				}
			}

			assertStagedContent(t, dir, testCase)
		})
	}
}

func TestNeutraliseBackendsRefusesAMalformedStagedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeStagedFile(t, dir, stagedMainFile, "terraform {\n  backend\n")

	if err := neutraliseBackends(dir); err == nil {
		t.Fatal("malformed staged source was accepted")
	}
}

func assertStagedContent(t *testing.T, dir string, testCase stripCase) {
	t.Helper()

	for name := range testCase.files {
		rewritten := readStagedFile(t, dir, name)

		for _, gone := range testCase.wantGone {
			if strings.Contains(rewritten, gone) {
				t.Fatalf("%s still contains %q: %s", name, gone, rewritten)
			}
		}

		for _, kept := range testCase.wantKept {
			if !strings.Contains(rewritten, kept) {
				t.Fatalf("%s lost %q: %s", name, kept, rewritten)
			}
		}
	}
}

func writeStagedFile(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), stagedFileMode); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func readStagedFile(t *testing.T, dir, name string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the test's own temporary directory.
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}

	return string(content)
}
