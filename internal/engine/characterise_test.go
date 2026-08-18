package engine_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M4.5a: the characterise tracer bullet. Every case drives the engine seam
// against the real Terraform binary, and every fixture is a module that has no
// test suite at all — the situation the mode exists for.

const (
	untestedAliasesFixture    = "untested-aliases"
	untestedZeroOutputFixture = "untested-zero-output"
	untestedSensitiveFixture  = "untested-sensitive"

	rungOutputs = "outputs"
)

// characteriseConfig is the base configuration for a characterisation.
func characteriseConfig(t *testing.T, moduleDir string) engine.Config {
	t.Helper()

	config := baseConfig(t, moduleDir)
	config.Characterise = true

	return config
}

// TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn is the first half
// of the M4.5 spec review's mandatory acceptance pair. The unscaffolded module
// mocks nothing, so a provider gate judged against it would refuse; judged
// against the effective staged suite it passes with no opt-in at all.
func TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	block := result.Characterisation
	if block == nil {
		t.Fatal("the report carries no characterisation block")
	}

	if !block.Complete {
		t.Fatalf("the characterisation is incomplete: %d pins", len(block.Pins))
	}

	pinned := block.PinsByStatus()[report.Pinned]
	if pinned == 0 {
		t.Fatal("no value was pinned")
	}

	if len(block.Files) != 1 {
		t.Fatalf("generated %d files, want one per scenario", len(block.Files))
	}

	content := block.Files[0].Content
	for _, wanted := range []string{
		`mock_provider "null" {`, `alias = "primary"`, `alias = "secondary"`,
		"state_key = ", "command   = apply",
	} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("the generated file does not contain %q:\n%s", wanted, content)
		}
	}
}

// TestAMissingAliasMockRefusesBeforeExecution is the pair's second half: the
// staged provider gate is decided per provider *configuration*, so removing one
// generated alias mock refuses, and refuses before Terraform evaluates
// anything.
func TestAMissingAliasMockRefusesBeforeExecution(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	config := characteriseConfig(t, module)
	config.SeedMissingMock = "null.secondary"

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a refusal for the unmocked provider configuration", err)
	}

	if !strings.Contains(err.Error(), "null.secondary") {
		t.Fatalf("the refusal does not name the configuration: %v", err)
	}

	if !strings.Contains(err.Error(), "--allow-real-infrastructure") {
		t.Fatalf("the refusal does not name the opt-in flag: %v", err)
	}
}

// TestTheDefaultCharacterisationWritesNothing holds the never-write contract
// over the new mode: without --write the suite exists as an overlay, and the
// module directory is byte-identical afterwards.
func TestTheDefaultCharacterisationWritesNothing(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)
	before := treeDigest(t, module)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	if !maps.Equal(treeDigest(t, module), before) {
		t.Fatal("characterising without --write changed the source tree")
	}

	if !result.Characterisation.Staged {
		t.Fatal("the report does not say the suite was staged")
	}
}

// TestAWrittenSuiteIsGreenAndRegistered proves the write protocol's happy path:
// the file lands where the naming contract says, the provenance registry
// records it, and the suite it wrote passes as an ordinary mutation baseline.
func TestAWrittenSuiteIsGreenAndRegistered(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	target := filepath.Join(module, "tests", "characterise_defaults.tftest.hcl")
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("the generated suite is not at the documented path: %v", statErr)
	}

	if result.Characterisation.Write == nil || len(result.Characterisation.Write.Written) != 1 {
		t.Fatalf("the report does not record the write: %+v", result.Characterisation.Write)
	}

	if _, statErr := os.Stat(filepath.Join(module, engine.RegistryName)); statErr != nil {
		t.Fatalf("no provenance registry was recorded: %v", statErr)
	}

	// The written suite is an ordinary suite: the mutation loop runs against it
	// without complaint, which is the whole point of generating one.
	graded, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("running the generated suite: %v", err)
	}

	if len(graded.Mutants) == 0 {
		t.Fatal("the generated suite graded no mutants")
	}
}

// TestASecondWriteIsRefusedAsACollision holds the collision rule over the full
// target path set.
func TestASecondWriteIsRefusedAsACollision(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrWriteRefused) {
		t.Fatalf("error = %v, want a collision refusal", err)
	}

	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("the refusal does not name the flag that would replace it: %v", err)
	}
}

// TestForceReplacesOnlyUnmodifiedGeneratedFiles is the provenance registry's
// contract: --force is permission to replace what this tool wrote and nobody
// touched, and nothing else.
func TestForceReplacesOnlyUnmodifiedGeneratedFiles(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	forced := config
	forced.CharacteriseForce = true

	if _, err := engine.Run(t.Context(), forced); err != nil {
		t.Fatalf("--force over an unmodified generated file: %v", err)
	}

	target := filepath.Join(module, "tests", "characterise_defaults.tftest.hcl")
	appendTo(t, target, "\n# a human edited this file\n")

	_, err := engine.Run(t.Context(), forced)
	if !errors.Is(err, engine.ErrWriteRefused) {
		t.Fatalf("error = %v, want --force to refuse an edited file", err)
	}

	if !strings.Contains(err.Error(), "edited") {
		t.Fatalf("the refusal does not say the file was edited: %v", err)
	}
}

// TestScenariosCarryDistinctStateKeys is the C3 repair: a generated scenario
// that shared implicit run-block state would pin an update where it claims to
// pin a create.
func TestScenariosCarryDistinctStateKeys(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	keys := map[string]bool{}

	for _, scenario := range result.Characterisation.Scenarios {
		if scenario.StateKey == "" {
			t.Fatalf("scenario %s carries no state key", scenario.Name)
		}

		if keys[scenario.StateKey] {
			t.Fatalf("state key %q is shared by two scenarios", scenario.StateKey)
		}

		keys[scenario.StateKey] = true
	}
}

// appendTo appends to an existing file, standing in for a human edit.
func appendTo(t *testing.T, path, addition string) {
	t.Helper()

	//nolint:gosec // a test-owned temporary path.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}

	if _, err := file.WriteString(addition); err != nil {
		_ = file.Close()

		t.Fatalf("appending to %s: %v", path, err)
	}

	if err := file.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
}

// TestAZeroOutputModuleEscalatesAndSaysSo is the C6 contract, which
// agent-integration.md recorded before the spec omitted it: the outputs rung
// on a module with no outputs would certify an empty suite.
func TestAZeroOutputModuleEscalatesAndSaysSo(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedZeroOutputFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	block := result.Characterisation
	if !block.Escalated {
		t.Fatal("a module with no outputs did not escalate away from the outputs rung")
	}

	if block.Rung != "counts" || block.RungRequested != rungOutputs {
		t.Fatalf("rung = %s (requested %s), want counts escalated from outputs",
			block.Rung, block.RungRequested)
	}

	if block.EscalationReason == "" {
		t.Fatal("the escalation does not say why")
	}

	if !block.Complete {
		t.Fatal("the escalated rung pinned nothing")
	}

	if !strings.Contains(block.Files[0].Content, "length(terraform_data.unit) == 2") {
		t.Fatalf("the counts rung pinned no instance count:\n%s", block.Files[0].Content)
	}
}

// TestARungThatPinsNothingIsNeverComplete holds the other half of the same
// contract: green with nothing pinned may not be reported as a finished
// characterisation, whatever rung produced it.
func TestARungThatPinsNothingIsNeverComplete(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedZeroOutputFixture)

	config := characteriseConfig(t, module)
	config.PinRung = rungOutputs
	config.SeedNoEscalation = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	if result.Characterisation.Complete {
		t.Fatal("a characterisation that pinned nothing reported complete")
	}
}

// TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined is the ladder's
// top level and its documented skip classes: a value the mock invented is
// never pinned, and a nested attribute has no type-correct dotted spelling.
func TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, untestedAliasesFixture)

	config := characteriseConfig(t, module)
	config.PinRung = "configured"

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	block := result.Characterisation
	counts := block.PinsByStatus()

	if counts[report.Pinned] == 0 {
		t.Fatal("the configured rung pinned nothing")
	}

	if counts[report.PinSkippedMockInvented] == 0 {
		t.Fatal("the configured rung pinned every computed attribute the mock invented")
	}

	for _, pin := range block.Pins {
		if pin.Status != report.Pinned && pin.Expression != "" {
			t.Fatalf("skipped pin %s carries executable content: %s", pin.ID, pin.Expression)
		}

		if pin.Status != report.Pinned && pin.Reason == "" {
			t.Fatalf("skipped pin %s carries no reason", pin.ID)
		}
	}

	if !strings.Contains(block.Files[0].Content, `terraform_data.anchor.input == "steady-dev"`) {
		t.Fatalf("the configured rung did not pin the value the configuration determined:\n%s",
			block.Files[0].Content)
	}
}

// TestAnUnknownRungIsRefused keeps the ladder closed.
func TestAnUnknownRungIsRefused(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedZeroOutputFixture)

	config := characteriseConfig(t, module)
	config.PinRung = "everything"

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, characterise.ErrRung) {
		t.Fatalf("error = %v, want a refusal of the unknown granularity", err)
	}
}

// TestASensitiveValueReachesNoGeneratedArtefact is the M8 widening applied to
// the pinning stage: a value Terraform marks sensitive is skipped, and neither
// it nor any rendering of it appears in a generated file, a report field or a
// rendered line.
func TestASensitiveValueReachesNoGeneratedArtefact(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedSensitiveFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	block := result.Characterisation
	if block.PinsByStatus()[report.PinSkippedSensitive] == 0 {
		t.Fatal("the sensitive output was not skipped")
	}

	const secret = "tfmut-characterise-secret"

	rendered := strings.Builder{}
	if err := report.WriteTerminal(&rendered, result); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	for name, artefact := range map[string]string{
		"the terminal rendering": rendered.String(),
		"the JSON report":        encoded.String(),
	} {
		if strings.Contains(artefact, secret) {
			t.Fatalf("%s carries the sensitive value", name)
		}
	}
}

// TestARealCharacterisationReportValidatesAgainstThePublishedSchema is the
// 2.3.0 contract proved over engine-produced documents rather than
// hand-written ones, per interesting shape.
func TestARealCharacterisationReportValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	shapes := map[string]func(engine.Config) engine.Config{
		"pinned": func(config engine.Config) engine.Config { return config },
		"escalated": func(config engine.Config) engine.Config {
			config.PinRung = rungOutputs

			return config
		},
		"skipped": func(config engine.Config) engine.Config {
			config.PinRung = "configured"

			return config
		},
	}

	fixtures := map[string]string{
		"pinned":    untestedSensitiveFixture,
		"escalated": untestedZeroOutputFixture,
		"skipped":   untestedSensitiveFixture,
	}

	for name, adjust := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, fixtures[name])

			result, err := engine.Run(t.Context(), adjust(characteriseConfig(t, module)))
			if err != nil {
				t.Fatalf("characterise: %v", err)
			}

			schema := loadPublishedSchema(t)

			builder := strings.Builder{}
			if err := report.WriteJSON(&builder, result); err != nil {
				t.Fatalf("rendering: %v", err)
			}

			document := any(nil)
			if err := json.Unmarshal([]byte(builder.String()), &document); err != nil {
				t.Fatalf("decoding: %v", err)
			}

			if problems := validateAgainst(schema, schema, document, "$"); len(problems) > 0 {
				t.Fatalf("the real report does not validate:\n  %s", strings.Join(problems, "\n  "))
			}
		})
	}
}
