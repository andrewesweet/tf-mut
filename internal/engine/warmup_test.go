package engine_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

const warmupOutputNullOperator = "EXT-OUTPUT-NULL"

// The two admissions here are about the workspace tf-mut stages before any
// test runs: init must be told the same test directory the test run will use,
// or the modules that directory's run blocks call are never installed; and a
// root that declares a backend must not block the provider schema, or the run
// dies before the baseline. Both fixtures are offline: terraform_data and a
// variable echo, no provider beyond the builtin.

// executedAny reports whether at least one mutant ran, which is the claim
// both staging admissions make: the staged workspace let the suite execute
// the population.
func executedAny(t *testing.T, result report.Report) bool {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.ExecutedRuns > 0 {
			return true
		}
	}

	return false
}

// TestUnitTestDirectoryModuleCallsAreAdmittedAndScored drives a suite whose
// unit tests live in tests/unit and call a module from a run block, configured
// through the test directory the way a caller of a suite laid out this way
// configures it. Terraform installs only the modules called by the test
// directory init is told about, so init without -test-directory leaves the
// call uninstalled and the baseline red with "Module not installed".
func TestUnitTestDirectoryModuleCallsAreAdmittedAndScored(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unit-directory-modulecall")

	config := baseConfig(t, module)
	config.TestDirectory = "tests/unit"

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("run produced no mutants")
	}

	if !executedAny(t, result) {
		t.Fatal("no mutant executed; the suite did not exercise the population")
	}
}

// TestDeclaredBackendDoesNotBlockTheSchemaOrTheSuite drives a root that
// declares backend "azurerm" with no configured credentials anywhere. Init
// runs with -backend=false, and the next command in that workspace then
// demands backend initialisation; the first is providers schema, so the run
// died before the baseline. Schema retrieval must not see the backend.
func TestDeclaredBackendDoesNotBlockTheSchemaOrTheSuite(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fixture string
		file    string
	}{
		{fixture: "backend-declared", file: mainFile},
		{fixture: "backend-json-object", file: "backend.tf.json"},
		{fixture: "backend-json-array", file: "backend.tf.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			t.Parallel()

			for _, assertion := range []string{"unasserted", "asserted"} {
				t.Run(assertion, func(t *testing.T) {
					t.Parallel()

					checkDeclaredBackend(t, testCase.fixture, testCase.file, assertion)
				})
			}
		})
	}
}

func checkDeclaredBackend(t *testing.T, fixture, file, assertion string) {
	t.Helper()

	module := copyFixture(t, fixture)
	//nolint:gosec // deliberate read-only fixture exercising the fresh-inode write path.
	if err := os.Chmod(filepath.Join(module, file), 0o444); err != nil {
		t.Fatalf("making backend file read-only: %v", err)
	}

	if assertion == "asserted" {
		assertBackendFileChecksum(t, module, file)
	}

	before := treeDigest(t, module)
	request := baseConfig(t, module)
	request.IncludeOperators = []string{warmupOutputNullOperator}
	request.NoCache = true

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertTreeUnchanged(t, module, before)
	assertBackendMutationFingerprint(t, result)
}

func assertBackendFileChecksum(t *testing.T, module, file string) {
	t.Helper()

	digest := treeDigest(t, module)[file]
	testsupport.WriteFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), fmt.Sprintf(`run "plans" {
  command = plan

  assert {
    condition     = terraform_data.checksum.input == %q
    error_message = "backend file changed"
  }
}
`, digest))
}

func assertBackendMutationFingerprint(t *testing.T, result report.Report) {
	t.Helper()

	if len(result.Mutants) != 1 {
		t.Fatalf("got %d mutants, want one output mutation", len(result.Mutants))
	}

	mutant := result.Mutants[0]
	if mutant.State != report.Survived || mutant.Verdict == nil || mutant.ExecutedRuns == 0 {
		t.Fatalf("output mutation did not survive execution: %+v", mutant)
	}

	if len(mutant.Verdict.Evidence.Delta) == 0 {
		t.Fatal("output mutation has no fingerprint delta")
	}

	for _, change := range mutant.Verdict.Evidence.Delta {
		if strings.Contains(change.Path, "terraform_data.checksum") {
			t.Fatalf("unmutated backend file changed the fingerprint: %+v", change)
		}
	}
}

func TestUnformattedBackendSourceIsWarnedAbout(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "backend-declared")
	path := filepath.Join(module, mainFile)
	testsupport.WriteFile(t, path, strings.Replace(readFile(t, path), "  backend", "    backend", 1))
	before := treeDigest(t, module)
	request := baseConfig(t, module)
	request.IncludeOperators = []string{warmupOutputNullOperator}

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(strings.Join(result.Warnings, " "), "formatted") {
		t.Fatalf("warnings do not mention formatting: %v", result.Warnings)
	}

	assertTreeUnchanged(t, module, before)
}

func TestProvidersSchemaFailureCarriesNoColourCodes(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "schema-uninitialised-cloud")
	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, tfexec.ErrCommandFailed) {
		t.Fatalf("error = %v, want a Terraform command failure", err)
	}

	if !strings.Contains(err.Error(), "providers schema") || !strings.Contains(err.Error(), "initialization required") {
		t.Fatalf("error = %v, want the schema's initialization demand", err)
	}

	if strings.Contains(err.Error(), "\x1b[") {
		t.Fatalf("error text carries ANSI colour codes: %q", err.Error())
	}
}

func TestJSONBackendNeutralisationKeepsRepeatedDeclarations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		document   string
		refuseInit bool
		preview    bool
	}{
		{
			name: "backend-first",
			document: `{"terraform":{"backend":{"azurerm":{}}},
"terraform":{"required_version":">= 1.6"}}`,
		},
		{
			name: "backend-last-retains-the-version-refusal",
			document: `{"terraform":{"required_version":"< 1.6"},
"terraform":{"backend":{"azurerm":{}}}}`,
			refuseInit: true,
		},
		{
			name: "repeated-terraform-array",
			document: `{"terraform":[{"backend":{"azurerm":{}}}],
"terraform":[{"required_version":">= 1.6"}]}`,
		},
		{
			name: "null-properties-around-backend",
			document: `{"terraform":null,"terraform":{"backend":{"azurerm":{}}},
"terraform":null}`,
		},
		{
			name:    "null-array-bodies-around-backend",
			preview: true,
			document: `{"terraform":[null,{"backend":{"azurerm":{}}},null,
{"required_version":">= 1.6"}]}`,
		},
		{
			name:    "root-array",
			preview: true,
			document: `[{"terraform":{"backend":{"azurerm":{}}}},
{"terraform":[{"required_version":">= 1.6"}]}]`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "backend-json-object")
			backend := filepath.Join(module, "backend.tf.json")
			testsupport.WriteFile(t, backend, testCase.document)
			//nolint:gosec // deliberate read-only fixture exercising the fresh-inode write path.
			if err := os.Chmod(backend, 0o444); err != nil {
				t.Fatalf("making backend read-only: %v", err)
			}

			assertBackendFileChecksum(t, module, "backend.tf.json")
			before := treeDigest(t, module)
			request := baseConfig(t, module)
			request.IncludeOperators = []string{warmupOutputNullOperator}
			request.NoCache = true
			var command engine.Request = request
			if testCase.preview {
				preview := previewRequest(t, module)
				preview.IncludeOperators = request.IncludeOperators
				command = preview
			}

			result, err := engine.Run(t.Context(), command)
			assertTreeUnchanged(t, module, before)

			assertJSONBackendResult(t, result, err, testCase.refuseInit, testCase.preview)
		})
	}
}

func assertJSONBackendResult(t *testing.T, result report.Report, err error, refuseInit, preview bool) {
	t.Helper()

	if refuseInit {
		if !errors.Is(err, tfexec.ErrCommandFailed) || !strings.Contains(err.Error(), "init:") ||
			!strings.Contains(err.Error(), "Unsupported Terraform Core version") {
			t.Fatalf("error = %v, want the declared version's init refusal", err)
		}

		return
	}

	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if preview {
		if len(result.Mutants) != 1 || executedAny(t, result) {
			t.Fatalf("preview did not describe an unexecuted output mutation: %+v", result.Mutants)
		}

		return
	}

	if len(result.Mutants) != 1 || result.Mutants[0].State != report.Survived || !executedAny(t, result) {
		t.Fatalf("output mutation did not survive execution: %+v", result.Mutants)
	}
}

func TestABackendlessJSONRootArrayIsAdmittedByPreview(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "backendless-json-root-array")
	before := treeDigest(t, module)
	preview, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if len(preview.Mutants) == 0 || executedAny(t, preview) {
		t.Fatalf("preview did not describe an unexecuted population: %+v", preview.Mutants)
	}

	assertTreeUnchanged(t, module, before)
}

func TestNullTerraformJSONBodiesAreAdmitted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		preview  bool
	}{
		{name: "property", document: "", preview: false},
		{
			name:     "repeated-properties",
			document: `{"terraform":null,"terraform":{"required_version":">= 1.6"},"terraform":null}`,
			preview:  false,
		},
		{
			name:     "array-bodies",
			document: `{"terraform":[null,{"required_version":">= 1.6"},null]}`,
			preview:  true,
		},
		{
			name:     "root-array",
			document: `[{"terraform":null},{"terraform":[null,{"required_version":">= 1.6"}]}]`,
			preview:  true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "backendless-json-null")
			if testCase.document != "" {
				testsupport.WriteFile(t, filepath.Join(module, "settings.tf.json"), testCase.document)
			}

			before := treeDigest(t, module)
			request := baseConfig(t, module)
			request.IncludeOperators = []string{warmupOutputNullOperator}
			request.NoCache = true
			var command engine.Request = request
			if testCase.preview {
				preview := previewRequest(t, module)
				preview.IncludeOperators = request.IncludeOperators
				command = preview
			}

			result, err := engine.Run(t.Context(), command)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			assertTreeUnchanged(t, module, before)

			if len(result.Mutants) != 1 {
				t.Fatalf("got %d mutants, want one output mutation", len(result.Mutants))
			}

			if testCase.preview {
				if executedAny(t, result) {
					t.Fatalf("preview executed an output mutation: %+v", result.Mutants)
				}

				return
			}

			if result.Mutants[0].State != report.Survived || !executedAny(t, result) {
				t.Fatalf("output mutation did not survive execution: %+v", result.Mutants)
			}
		})
	}
}

func TestBackendOverridesKeepClearedCloudDeclarationsOutOfWarmUp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fixture  string
		primary  string
		override string
	}{
		{fixture: "backend-cloud-override", primary: mainFile, override: "override.tf"},
		{fixture: "backend-cloud-json-object", primary: "cloud.tf.json", override: "state_override.tf.json"},
		{fixture: "backend-cloud-json-array", primary: "cloud.tf.json", override: "state_override.tf"},
		{fixture: "backend-cloud-hcl-json-override", primary: mainFile, override: "override.tf.json"},
		{fixture: "backend-cloud-earlier-override", primary: "cloud_override.tf", override: "state_override.tf"},
	}
	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, testCase.fixture)
			for _, file := range []string{testCase.primary, testCase.override} {
				//nolint:gosec // deliberate read-only fixture exercising the fresh-inode write path.
				if err := os.Chmod(filepath.Join(module, file), 0o444); err != nil {
					t.Fatalf("making %s read-only: %v", file, err)
				}
			}

			digests := treeDigest(t, module)
			testsupport.WriteFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), fmt.Sprintf(`run "plans" {
  command = plan

  assert {
    condition     = terraform_data.cloud_checksum.input == %q
    error_message = "primary cloud file changed"
  }

  assert {
    condition     = terraform_data.backend_checksum.input == %q
    error_message = "override backend file changed"
  }
}
`, digests[testCase.primary], digests[testCase.override]))
			before := treeDigest(t, module)
			request := baseConfig(t, module)
			request.IncludeOperators = []string{warmupOutputNullOperator}
			request.NoCache = true
			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			if len(result.Mutants) != 1 || result.Mutants[0].State != report.Survived || !executedAny(t, result) {
				t.Fatalf("output mutation did not survive execution: %+v", result.Mutants)
			}

			assertTreeUnchanged(t, module, before)
		})
	}
}

func TestOnlyEffectiveBackendOverridesSuppressCloud(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "empty-backend-block-array",
			files: map[string]string{
				"override.tf.json": `{"terraform":[{"backend":[{"local":[]}]}]}`,
			},
		},
		{
			name: "ignored-backend-override",
			files: map[string]string{
				".state_override.tf": "terraform {\n  backend \"local\" {}\n}\n",
			},
		},
		{
			name: "later-cloud-override",
			files: map[string]string{
				"a_override.tf":      "terraform {\n  backend \"local\" {}\n}\n",
				"z_override.tf.json": `{"terraform":[{"cloud":{}}]}`,
			},
		},
		{
			name: "same-file-cloud-override",
			files: map[string]string{
				"override.tf": "terraform {\n  backend \"local\" {}\n  cloud {}\n}\n",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "schema-uninitialised-cloud")
			for name, content := range testCase.files {
				testsupport.WriteFile(t, filepath.Join(module, name), content)
			}

			before := treeDigest(t, module)
			_, err := engine.Run(t.Context(), baseConfig(t, module))
			if !errors.Is(err, tfexec.ErrCommandFailed) || !strings.Contains(err.Error(), "providers schema") ||
				!strings.Contains(err.Error(), "initialization required") {
				t.Fatalf("error = %v, want the effective cloud's schema initialization demand", err)
			}

			assertTreeUnchanged(t, module, before)
		})
	}
}
