package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestUnmockedProviderIsRefusedWithoutTheFlag(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unmocked")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("refusal does not name the unmocked provider: %v", err)
	}

	if !strings.Contains(err.Error(), "--allow-real-infrastructure") {
		t.Fatalf("refusal does not name the opt-in flag: %v", err)
	}
}

func TestUnmockedProviderRunsWithTheFlag(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "unmocked")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}

	if len(result.Warnings) == 0 {
		t.Fatal("running against an unmocked provider must warn")
	}
}

func TestProvisionerIsRefusedAndNeverExecuted(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal", err)
	}

	if !strings.Contains(err.Error(), "provisioner") {
		t.Fatalf("refusal does not name the offending construct: %v", err)
	}

	if !strings.Contains(err.Error(), "main.tf:") {
		t.Fatalf("refusal does not carry a source range: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the provisioner executed despite the refusal")
	}
}

func TestProvisionerRunsOnlyWithTheSecondOptIn(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.AllowUnsandboxedEffects = true
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the opt-in did not let the apply-mode run execute: %v", statErr)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}
}

func TestPlanOnlySuiteWithEffectsStillProceeds(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	rewritePlanMode(t, filepath.Join(module, "tests", "unit.tftest.hcl"))

	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("a plan-mode suite must not be refused: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a plan-mode run executed the provisioner")
	}
}

// rewritePlanMode converts the fixture's apply-mode run to plan mode, which is
// the severable remainder the effects gate must let through.
func rewritePlanMode(t *testing.T, path string) {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // the path is a test-owned fixture copy.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	rewritten := strings.Replace(string(content), "command = apply", "command = plan", 1)

	//nolint:gosec // the path is a test-owned fixture copy.
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestSourceTreeIsUntouchedByEveryRun(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"skeleton", discriminateFixture, "count-tolerant", "foreach"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, fixture)
			before := treeDigest(t, module)

			if _, err := engine.Run(t.Context(), baseConfig(t, module)); err != nil {
				t.Fatalf("run: %v", err)
			}

			assertTreeUnchanged(t, module, before)
		})
	}
}

func TestKilledMutantsNeverPayForValidation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.State != report.Killed && mutant.State != report.Survived {
			continue
		}

		if mutant.Validated {
			t.Fatalf("mutant %s (%s) paid for terraform validate", mutant.ID, mutant.State)
		}
	}
}

func TestUnmockedProviderRefusalNamesTheOffendingBlock(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unmocked")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if err == nil {
		t.Fatal("expected a refusal")
	}

	if !strings.Contains(err.Error(), "null_resource.app") {
		t.Fatalf("refusal does not name the offending block: %v", err)
	}

	if !strings.Contains(err.Error(), "main.tf:") {
		t.Fatalf("refusal does not carry a source range: %v", err)
	}
}

func TestPreviewIsNeverRefusedBecauseItExecutesNothing(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.Preview = true
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview must not be refused: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("preview generated no mutants")
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("preview executed the provisioner")
	}
}

func TestResourcesWithoutAnExtremeMutantAreReported(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "no-optional-arguments")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	joined := strings.Join(result.Warnings, " ")
	if !strings.Contains(joined, "terraform_data.bare") {
		t.Fatalf("a resource no extreme mutant could reach was not reported: %v", result.Warnings)
	}

	for _, finding := range result.Findings {
		if finding.Address == "terraform_data.bare" {
			t.Fatal("a resource with no executed extreme mutant must not be called pseudo-tested")
		}
	}
}
