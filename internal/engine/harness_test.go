package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The fixtures are Terraform modules, so every test here drives the real
// Terraform binary through the engine seam. Nothing stubs Terraform: the
// correctness risks this milestone repairs all live in its real behaviour.

const (
	fixtureRoot = "testdata"
	testJobs    = 2

	// checkpointDisabled and inAutomation keep Terraform from phoning home and
	// from expecting a human at the terminal.
	checkpointDisabled = "CHECKPOINT_DISABLE=1"
	inAutomation       = "TF_IN_AUTOMATION=1"
)

// copyFixture copies a fixture into a temporary directory so that the tree the
// engine reads is disposable, and so a violation of the never-write contract
// cannot damage the checked-in corpus.
func copyFixture(t *testing.T, name string) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(target, os.DirFS(filepath.Join(fixtureRoot, name))); err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}

	return target
}

// baseConfig is the configuration every test starts from.
func baseConfig(t *testing.T, moduleDir string) engine.Config {
	t.Helper()

	return engine.Config{
		ModuleDir:               moduleDir,
		TestDirectory:           engine.DefaultTestDirectory,
		Jobs:                    testJobs,
		TimeoutFactor:           engine.DefaultTimeoutFactor,
		TimeoutFloor:            0,
		MinScore:                0,
		HasMinScore:             false,
		AllowIncompleteScore:    false,
		AllowRealInfrastructure: false,
		AllowUnsandboxedEffects: false,
		Preview:                 false,
		TerraformBinary:         "",
		Env:                     terraformEnv(t),
		WorkDir:                 t.TempDir(),
		TestSelection:           nil,
	}
}

// terraformEnv points Terraform at the repository's offline provider mirror
// when one has been installed, and keeps it from phoning home.
func terraformEnv(t *testing.T) []string {
	t.Helper()

	environment := []string{
		checkpointDisabled,
		inAutomation,
		// Terraform's plugin cache is not safe for concurrent writers, and
		// these tests run in parallel; each gets its own.
		"TF_PLUGIN_CACHE_DIR=" + t.TempDir(),
	}

	if config, found := cliConfigFile(t); found {
		environment = append(environment, "TF_CLI_CONFIG_FILE="+config)
	}

	return environment
}

func cliConfigFile(t *testing.T) (string, bool) {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		return "", false
	}

	path := filepath.Join(root, ".tools", "terraform-cli.tfrc")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}

	return path, true
}

func repositoryRoot(t *testing.T) (string, bool) {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, true
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return "", false
		}

		directory = parent
	}
}

// requireProviderMirror skips a test that needs a downloadable provider when
// the repository's offline mirror has not been installed.
func requireProviderMirror(t *testing.T) {
	t.Helper()

	if _, found := cliConfigFile(t); !found {
		t.Skip("provider mirror absent: run `just tools-install` to enable provider-backed fixtures")
	}
}

// treeDigest fingerprints every file in a directory, so a test can prove the
// source tree was not written to.
func treeDigest(t *testing.T, root string) map[string]string {
	t.Helper()

	digests := map[string]string{}

	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		content, err := os.ReadFile(path) //nolint:gosec // test-owned tree.
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		sum := sha256.Sum256(content)
		digests[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])

		return nil
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("digesting %s: %v", root, err)
	}

	return digests
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()

	after := treeDigest(t, root)

	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("source file %s changed during the run", path)
		}
	}

	for path := range after {
		if _, found := before[path]; !found {
			t.Fatalf("run created %s inside the source tree", path)
		}
	}
}

// stateOf returns the state of the mutant at the given site.
func stateOf(t *testing.T, result report.Report, site string) report.State {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.Site == site {
			return mutant.State
		}
	}

	t.Fatalf("no mutant at site %q; sites are %s", site, strings.Join(sites(result), ", "))

	return ""
}

func sites(result report.Report) []string {
	found := make([]string, 0, len(result.Mutants))

	for _, mutant := range result.Mutants {
		found = append(found, mutant.Operator+" "+mutant.Site)
	}

	slices.Sort(found)

	return found
}

func mutantsWithOperator(result report.Report, operator string) []report.Mutant {
	matching := []report.Mutant{}

	for _, mutant := range result.Mutants {
		if mutant.Operator == operator {
			matching = append(matching, mutant)
		}
	}

	return matching
}

func findingAddresses(result report.Report) []string {
	addresses := make([]string, 0, len(result.Findings))

	for _, finding := range result.Findings {
		addresses = append(addresses, finding.Address)
	}

	slices.Sort(addresses)

	return addresses
}

// stubTerraform writes an executable that answers only the version handshake.
// It exists so that the version refusal can be proved without installing an
// obsolete Terraform release; no other test replaces the real binary.
func stubTerraform(t *testing.T, version string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "terraform-stub")
	script := "#!/usr/bin/env bash\n" +
		"if [ \"$2\" = \"version\" ]; then\n" +
		"  printf '{\"terraform_version\":\"" + version + "\",\"platform\":\"linux_amd64\"}\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the stub must be executable.
		t.Fatalf("writing terraform stub: %v", err)
	}

	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // test-owned fixture copy.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // fixture copy under t.TempDir.
		t.Fatalf("writing %s: %v", path, err)
	}
}

// runFixture copies a fixture and runs the engine over it.
func runFixture(t *testing.T, name string) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, name)))
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}

	return result
}

// survivorsAt lists the survivors whose site is the given address.
func survivorsAt(result report.Report, site string) []report.Mutant {
	matching := []report.Mutant{}

	for _, mutant := range result.Survivors() {
		if mutant.Site == site {
			matching = append(matching, mutant)
		}
	}

	return matching
}

// verdicts renders the population as identifier, state and diagnosis, which is
// the tuple every determinism assertion compares.
func verdicts(result report.Report) []string {
	rendered := make([]string, 0, len(result.Mutants))

	for _, mutant := range result.Mutants {
		diagnosis := ""
		if mutant.Verdict != nil {
			diagnosis = string(mutant.Verdict.Diagnosis)
		}

		rendered = append(rendered, mutant.ID+" "+string(mutant.State)+" "+diagnosis)
	}

	slices.Sort(rendered)

	return rendered
}

// statesOnly drops the identifiers, which a file rename legitimately changes.
func statesOnly(result report.Report) []string {
	rendered := make([]string, 0, len(result.Mutants))

	for _, mutant := range result.Mutants {
		diagnosis := ""
		if mutant.Verdict != nil {
			diagnosis = string(mutant.Verdict.Diagnosis)
		}

		rendered = append(rendered, mutant.Operator+" "+mutant.Site+" "+
			string(mutant.State)+" "+diagnosis)
	}

	slices.Sort(rendered)

	return rendered
}

func diagnoses(result report.Report) map[report.Diagnosis]int {
	return result.Metrics.Diagnoses
}

func renameFile(t *testing.T, dir, from, to string) {
	t.Helper()

	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		t.Fatalf("renaming %s: %v", from, err)
	}
}

// validateAgainst is the same deliberately small JSON Schema checker the report
// package uses, covering type, required, properties, items, enum, const and
// local $ref.
//
// The published SARIF schema uses more vocabulary than that — anyOf, patterns,
// additionalProperties — so this is a partial check by construction. What it
// does cover is the part that matters here: that the document has SARIF's
// required members, at SARIF's types, with SARIF's enumerated levels.
func validateAgainst(root, schema map[string]any, document any, path string) []string {
	if reference, ok := schema["$ref"].(string); ok {
		return validateAgainst(root, resolveRef(root, reference), document, path)
	}

	problems := []string{}

	if expected, ok := schema["type"].(string); ok && !hasType(expected, document) {
		return []string{fmt.Sprintf("%s: want type %s, got %T", path, expected, document)}
	}

	if allowed, ok := schema["enum"].([]any); ok && !slices.Contains(allowed, document) {
		problems = append(problems, fmt.Sprintf("%s: %v is not one of %v", path, document, allowed))
	}

	switch typed := document.(type) {
	case map[string]any:
		problems = append(problems, validateMembers(root, schema, typed, path)...)
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for index, element := range typed {
				problems = append(problems,
					validateAgainst(root, items, element, fmt.Sprintf("%s[%d]", path, index))...)
			}
		}
	default:
	}

	return problems
}

func validateMembers(root, schema, document map[string]any, path string) []string {
	problems := []string{}

	if required, ok := schema["required"].([]any); ok {
		for _, entry := range required {
			key, isString := entry.(string)
			if _, present := document[key]; isString && !present {
				problems = append(problems, fmt.Sprintf("%s: missing required property %q", path, key))
			}
		}
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return problems
	}

	for key, value := range document {
		described, known := properties[key].(map[string]any)
		if !known {
			continue
		}

		problems = append(problems, validateAgainst(root, described, value, path+"."+key)...)
	}

	return problems
}

func resolveRef(root map[string]any, reference string) map[string]any {
	current := root

	for segment := range strings.SplitSeq(strings.TrimPrefix(reference, "#/"), "/") {
		next, ok := current[segment].(map[string]any)
		if !ok {
			return map[string]any{}
		}

		current = next
	}

	return current
}

func hasType(expected string, document any) bool {
	switch expected {
	case "object":
		_, ok := document.(map[string]any)

		return ok
	case "array":
		_, ok := document.([]any)

		return ok
	case "string":
		_, ok := document.(string)

		return ok
	case "boolean":
		_, ok := document.(bool)

		return ok
	case "number", "integer":
		_, ok := document.(float64)

		return ok
	default:
		return true
	}
}
