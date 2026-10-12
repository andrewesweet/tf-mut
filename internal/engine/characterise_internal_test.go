package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
	"github.com/andrewesweet/tf-mut/internal/tracing"
)

// The staged characterisation pipeline's pin-defect proofs.
//
// The two pin-defect hooks are fields on characteriseStage, and production
// never writes one, so these tests cannot reach the hooks through the seam
// entry. They run the pipeline's own steps — the constructor's preparation,
// then scaffoldSuite and closeTheGap — over a real fixture with the real
// Terraform binary, and keep the assertions the seam tests made: the seeded
// pin set is refused as not green, and nothing reaches the module's tree.

// untestedBranchesFixture is the conditional-flipping fixture both seeded
// pin-defect tests run over: terraform_data-based, offline and fast.
const untestedBranchesFixture = "untested-branches"

// copyFixture copies a fixture into a temporary directory so that the tree
// the pipeline reads is disposable. It mirrors the external harness's helper
// of the same name: the two engine test packages cannot share helpers.
func copyFixture(t *testing.T, name string) string {
	t.Helper()

	target := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(target, os.DirFS(filepath.Join("testdata", name))); err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}

	return target
}

// stagedRequest is the characterisation request the staged tests start from,
// mirroring the external harness's request builders: real Terraform, a
// disposable work directory, and the hermetic environment the rest of the
// suite runs under.
func stagedRequest(t *testing.T, moduleDir string) CharacteriseRequest {
	t.Helper()

	return CharacteriseRequest{Common: Common{
		ModuleDir:               moduleDir,
		TestDirectory:           DefaultTestDirectory,
		Jobs:                    2,
		TimeoutFactor:           DefaultTimeoutFactor,
		TimeoutFloor:            0,
		AllowRealInfrastructure: false,
		AllowUnsandboxedEffects: false,
		TerraformBinary:         "",
		Env: []string{
			"CHECKPOINT_DISABLE=1",
			"TF_IN_AUTOMATION=1",
			// Terraform's plugin cache is not safe for concurrent writers,
			// and each test gets its own.
			"TF_PLUGIN_CACHE_DIR=" + t.TempDir(),
		},
		WorkDir:     t.TempDir(),
		ToolVersion: "",
		SetFlags:    nil,
	}}
}

// newStagedCharacterisation is the internal test constructor for the staged
// characterisation pipeline: the same preparation characteriseModule runs, in
// the same order — settings from the request, the version gate, discovery,
// the staged safety gates against the planned mocks, warm-up and the scaffold
// plan — ending in the stage the pipeline functions read. It exists so a test
// can set the pin-defect hooks, which production never writes; if the
// preparation in characteriseModule changes, this constructor changes with it.
func newStagedCharacterisation(
	ctx context.Context, t *testing.T, request CharacteriseRequest,
) (characteriseStage, error) {
	t.Helper()

	s, err := settingsFor(request)
	if err != nil {
		return characteriseStage{}, err
	}

	moduleDir, err := filepath.Abs(s.ModuleDir)
	if err != nil {
		return characteriseStage{}, fmt.Errorf("resolving module directory: %w", err)
	}

	s, configured, err := s.withConfiguration(moduleDir)
	if err != nil {
		return characteriseStage{}, err
	}

	s, err = finalise(s, configured)
	if err != nil {
		return characteriseStage{}, err
	}

	runner := tfexec.Runner{Binary: s.TerraformBinary, Env: s.Env}

	version, err := checkVersion(ctx, runner, moduleDir)
	if err != nil {
		return characteriseStage{}, err
	}

	configuration, err := discovery.DiscoverWith(moduleDir, s.TestDirectory,
		discovery.Options{SkipJSON: disableJSONReading(s), CheckoutRoot: checkoutRoot(ctx, moduleDir)})
	if err != nil {
		return characteriseStage{}, err
	}

	collector := newRunCollector(ctx, tracing.New(ctx, s.OTLPEndpoint, s.Progress))

	rung, answers, err := characteriseInputs(configuration, s)
	if err != nil {
		return characteriseStage{}, err
	}

	// The gates first, and nothing before them but the version check — the
	// same judgement against the effective staged suite the entry makes.
	mockOptions := characterise.Options{TestDirRel: configuration.TestDirRelative()}
	gated := characterise.Plan(configuration, tfexec.Schemas{}, mockOptions,
		characterise.Configurations(configuration))

	if _, err = checkStagedSafety(configuration, seedMissingMock(configuration, gated), s); err != nil {
		return characteriseStage{}, err
	}

	workRoot, err := os.MkdirTemp(s.WorkDir, "tf-mut-")
	if err != nil {
		return characteriseStage{}, fmt.Errorf("creating work directory: %w", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(workRoot) })

	prepared, err := warmUp(ctx, runner, configuration, workRoot, collector)
	if err != nil {
		return characteriseStage{}, err
	}

	scaffold, err := planScaffold(configuration, s, prepared, rung, answers)
	if err != nil {
		return characteriseStage{}, err
	}

	return characteriseStage{
		configuration: configuration, settings: s,
		prepared: prepared, workRoot: workRoot, terraform: version, collector: collector,
		runner: runner, scaffold: scaffold, answers: answers,
		scaffolds: newScaffoldSet(),
	}, nil
}

// seedOneFalsePin is the defect both proofs seed: one pin nothing could have
// harvested, appended to the set the stage passes through.
func seedOneFalsePin(marker string) func([]characterise.Pin) []characterise.Pin {
	return func(pins []characterise.Pin) []characterise.Pin {
		if len(pins) == 0 {
			return pins
		}

		defect := pins[0]

		return append(slices.Clone(pins), characterise.Pinned(
			defect.Scenario(), defect.Address(),
			defect.Address()+` == "`+marker+`"`, defect.Rung(),
		))
	}
}

// TestASeededInitialPinDefectIsRejectedBeforeAnythingIsWritten holds the other
// half of issue #74's acceptance pair: the verification between the harvest and
// everything downstream of it.
//
// The final-pin defect hook proves the verifier after the until-dry loop. This one
// proves the verifier before it — a separate call, on a path a run without
// `--until-dry` takes, and one that a deletion would have left every test green
// over. The assertions are the seam test's: the seeded pin set is refused as
// not green, and no file and no provenance registry reach the module.
func TestASeededInitialPinDefectIsRejectedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedBranchesFixture)

	request := stagedRequest(t, module)
	request.Write = true

	stage, err := newStagedCharacterisation(t.Context(), t, request)
	if err != nil {
		t.Fatalf("staging the characterisation: %v", err)
	}

	stage.seedInitialPinDefect = seedOneFalsePin("tf-mut-seeded-initial-pin-defect")

	var block report.Characterisation
	if _, _, err := scaffoldSuite(t.Context(), stage, &block); !errors.Is(err, ErrScaffoldRed) {
		t.Fatalf("error = %v, want the harvested pin set refused as not green", err)
	}

	entries, readErr := os.ReadDir(filepath.Join(module, "tests"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("reading the test directory: %v", readErr)
	}

	if len(entries) > 0 {
		t.Fatalf("a refused verification still wrote %d file(s)", len(entries))
	}

	if _, statErr := os.Stat(filepath.Join(module, RegistryName)); statErr == nil {
		t.Fatal("a refused verification still wrote the provenance registry")
	}
}

// TestTheFinalPinSetIsVerifiedBeforeAnyWrite is the write contract on the
// loop's other exit, asserted by seeding a defect rather than by observing a
// success.
//
// Each round proves the previous round's pins by baselining them at its start,
// so on the `dry` exit everything written was baselined. On the `bounded` exit
// there is no next round, and the pins the last round added would reach disk
// verified only one suggestion at a time — evidence, not the claim the
// contract makes. A pin nothing could have harvested is added to the final
// set: the verification between the loop and the write has to catch it, and
// nothing may be written. Remove that verification and this test fails.
//
//nolint:paralleltest // owns the package-global until-dry round hook for its lifetime.
func TestTheFinalPinSetIsVerifiedBeforeAnyWrite(t *testing.T) {
	module := copyFixture(t, untestedBranchesFixture)

	request := stagedRequest(t, module)
	request.UntilDry = true
	request.Write = true
	SetUntilDryRounds(t, module, 1)

	stage, err := newStagedCharacterisation(t.Context(), t, request)
	if err != nil {
		t.Fatalf("staging the characterisation: %v", err)
	}

	stage.seedFinalPinDefect = seedOneFalsePin("tf-mut-seeded-final-pin-defect")

	var block report.Characterisation
	pins, _, err := scaffoldSuite(t.Context(), stage, &block)
	if err != nil {
		t.Fatalf("scaffolding the suite: %v", err)
	}

	if _, _, err := closeTheGap(t.Context(), stage, &block, pins); !errors.Is(err, ErrScaffoldRed) {
		t.Fatalf("error = %v, want the final verification to catch the seeded pin", err)
	}

	entries, readErr := os.ReadDir(filepath.Join(module, "tests"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("reading the test directory: %v", readErr)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tftest.hcl") {
			t.Fatalf("an unverified suite was written: %s", entry.Name())
		}
	}
}
