package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M4c slice: the same seven M4.0 cases, re-proven with the JSON read.
//
// Every gate that fired from unreadness in `jsonfloor_test.go` fires here from
// content, and the ones whose content is innocent stop firing altogether. That
// pairing is the point: a floor that never lifts is indistinguishable from a
// refusal to support the syntax, and a lift that never fired is a fail-open
// edge with a comment on it.

const (
	jsonPartialFixture    = "json-partial"
	jsonUnmodelledFixture = "json-unmodelled"
)

func TestTheSliceFiresTheRealInfrastructureGateFromContent(t *testing.T) {
	t.Parallel()

	_, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonProviderFixture)))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal from the JSON provider inventory", err)
	}

	for _, expected := range []string{"null", "null_resource.side", jsonProvidersFileName} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("refusal does not name %s: %v", expected, err)
		}
	}

	if strings.Contains(err.Error(), "unread JSON") {
		t.Fatalf("the refusal came from unreadness rather than from content: %v", err)
	}
}

func TestTheSliceFiresTheEffectsGateFromContent(t *testing.T) {
	t.Parallel()

	config := baseConfig(t, copyFixture(t, jsonProvisionerFixture))

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal from the JSON effect inventory", err)
	}

	for _, expected := range []string{"provisioner", "terraform_data.side", jsonEffectsFileName} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("refusal does not name %s: %v", expected, err)
		}
	}
}

func TestTheSliceReadsMockStatusFromAJSONTestFile(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonTestMockFixture)))
	if err != nil {
		t.Fatalf("a JSON-declared mock_provider must satisfy the gate: %v", err)
	}

	if result.Baseline.Runs == 0 {
		t.Fatal("the JSON-declared run block did not execute")
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}
}

func TestTheSliceLetsTheEvaluatorReadAJSONVariablesFile(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonAutoVarFixture)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertNoFloorWarning(t, result)

	// The whole-block mutants stay executed by the multiplicity guard, exactly
	// as they do over an HCL `terraform.tfvars`; what the read file decides is
	// the body mutants.
	gated := 0

	for _, mutant := range bodyMutants(result, gatedResource, "count") {
		if mutant.Site == gatedResource {
			continue
		}

		gated++

		if mutant.State != report.NoCoverage {
			t.Fatalf("body mutant %s at %s = %s, want NoCoverage: terraform.tfvars.json "+
				"proves the multiplicity zero", mutant.ID, mutant.Site, mutant.State)
		}
	}

	if gated == 0 {
		t.Fatal("the fixture generated no body mutant inside the gated block")
	}
}

// TestTheSliceRepairsTheMixedModuleFalseProof is issue #57's second
// reproduction under the slice: the JSON-declared reader now draws its edge, so
// the local's cone reaches an observable and the shortcut correctly declines.
func TestTheSliceRepairsTheMixedModuleFalseProof(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	configuration, err := discovery.Discover(
		filepath.Join(fixtureRoot, jsonMixedFixture), engine.DefaultTestDirectory)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	cone, mapped := configuration.BuildGraph().SiteCone(".", "local.json_only")
	if !mapped {
		t.Fatal("local.json_only does not map into the graph")
	}

	if !cone.ContainsObservable() {
		t.Fatal("the repaired cone still reaches no observable: the JSON edge is missing")
	}

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonMixedFixture)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertNoFloorWarning(t, result)

	if state := stateOf(t, result, "local.json_only"); state != report.Killed {
		t.Fatalf("local.json_only mutant = %s, want Killed", state)
	}
}

func TestAParseFailureRetainsTheFloorWhileItsNeighboursLiftTheirs(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, jsonPartialFixture)

	if _, err := engine.Run(t.Context(), baseConfig(t, module)); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v: an unreadable neighbour must keep the floor down", err)
	}

	if err := os.Remove(filepath.Join(module, "broken.tf.json")); err != nil {
		t.Fatalf("removing the unreadable file: %v", err)
	}

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("with only readable JSON left the run must proceed: %v", err)
	}

	assertNoFloorWarning(t, result)

	if state := stateOf(t, result, "local.json_only"); state != report.Killed {
		t.Fatalf("local.json_only mutant = %s, want Killed: the readable neighbour lifted its floor", state)
	}
}

func TestWellFormedJSONThisVersionCannotModelRetainsTheFloor(t *testing.T) {
	t.Parallel()

	_, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonUnmodelledFixture)))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v: unmodelled content is never permission", err)
	}

	if !strings.Contains(err.Error(), "some_future_top_level_construct") {
		t.Fatalf("the refusal does not name what it could not model: %v", err)
	}
}

func TestNoJSONFileIsEverAMutationSite(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{jsonMixedFixture, jsonAutoVarFixture, jsonProviderFixture} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			config := baseConfig(t, copyFixture(t, fixture))
			config.Preview = true

			result, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}

			for _, mutant := range result.Mutants {
				if strings.HasSuffix(mutant.Range.File, ".json") {
					t.Fatalf("mutant %s has a JSON mutation site at %s", mutant.ID, mutant.Range.File)
				}
			}
		})
	}
}

// TestAJSONPopulationIsUnchangedByReadingIt keeps the discover-only promise
// honest at the population level: reading JSON may change verdicts through the
// graph and the inventories, and must never add or remove a mutant.
func TestAJSONPopulationIsUnchangedByReadingIt(t *testing.T) {
	t.Parallel()

	read := baseConfig(t, copyFixture(t, jsonMixedFixture))
	read.Preview = true

	unread := baseConfig(t, copyFixture(t, jsonMixedFixture))
	unread.Preview = true
	unread.DisableJSONReading = true

	withJSON, err := engine.Run(t.Context(), read)
	if err != nil {
		t.Fatalf("preview with JSON read: %v", err)
	}

	withoutJSON, err := engine.Run(t.Context(), unread)
	if err != nil {
		t.Fatalf("preview with JSON unread: %v", err)
	}

	if strings.Join(verdicts(withJSON), "\n") != strings.Join(verdicts(withoutJSON), "\n") {
		t.Fatalf("reading JSON changed the population:\nread:   %v\nunread: %v",
			verdicts(withJSON), verdicts(withoutJSON))
	}
}

func TestAChangedJSONConfigurationIsACacheKeyDimension(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, jsonMixedFixture)

	first, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	if first.Population.Cached != 0 {
		t.Fatalf("a first run served %d verdicts from the cache", first.Population.Cached)
	}

	writeFile(t, filepath.Join(module, "side.tf.json"), strings.Replace(
		readFile(t, filepath.Join(module, "side.tf.json")),
		"read-from-json", "changed-in-json", 1))

	second, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if second.Population.Cached != 0 {
		t.Fatalf("a changed .tf.json reused %d cached verdicts: the key does not hash it",
			second.Population.Cached)
	}
}

func assertNoFloorWarning(t *testing.T, result report.Report) {
	t.Helper()

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "unread JSON") {
			t.Fatalf("the slice left the floor down: %q", warning)
		}
	}
}

// TestExclusionsCannotHideJSONDeclaredContentUnderTheSlice re-proves the
// exclusions case with the content read: the safety inventories are computed
// before exclusion, so no configured exclusion reaches a JSON-declared effect.
func TestExclusionsCannotHideJSONDeclaredContentUnderTheSlice(t *testing.T) {
	t.Parallel()

	for _, exclusion := range []string{"*.json", "*", "effects.tf.json"} {
		t.Run(exclusion, func(t *testing.T) {
			t.Parallel()

			config := baseConfig(t, copyFixture(t, jsonProvisionerFixture))
			config.AllowRealInfrastructure = true
			config.ExcludePaths = []string{exclusion}

			_, err := engine.Run(t.Context(), config)
			if !errors.Is(err, engine.ErrUnsandboxedEffects) {
				t.Fatalf("exclusion %q hid the JSON-declared provisioner: error = %v", exclusion, err)
			}
		})
	}
}

// TestNoTerraformRunPrecedesAContentDrivenRefusal re-proves the zero-runs case
// with the content read: a refusal decided from the JSON-declared inventory is
// as free as one decided from unreadness.
func TestNoTerraformRunPrecedesAContentDrivenRefusal(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "terraform-calls")

	config := baseConfig(t, copyFixture(t, jsonProviderFixture))
	config.TerraformBinary = recordingTerraform(t, log)

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a refusal", err)
	}

	for _, invocation := range terraformInvocations(t, log) {
		if invocation != "version" {
			t.Fatalf("terraform %s ran before the content-driven refusal", invocation)
		}
	}
}

// TestAJSONDeclaredModuleCallJoinsTheClosure is the PR #69 review's critical
// reproduction: the only calls to both children live in a `.tf.json`, so a
// closure followed through the HCL syntax alone would inventory neither the
// child's unmocked provider nor its provisioner — and both gates would fail
// open through the side door the floor exists to close.
func TestAJSONDeclaredModuleCallJoinsTheClosure(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "json-module")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	// The provider gate first: the JSON-called child requires an unmocked null.
	noFlags := baseConfig(t, module)
	noFlags.Env = append(noFlags.Env, "TF_MUT_MARKER="+marker)

	_, err := engine.Run(t.Context(), noFlags)
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal from the JSON-called child", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("the refusal does not name the child's provider: %v", err)
	}

	// The effects gate next, independently: the other JSON-called child
	// carries a provisioner.
	realOnly := baseConfig(t, module)
	realOnly.AllowRealInfrastructure = true
	realOnly.Env = append(realOnly.Env, "TF_MUT_MARKER="+marker)

	_, err = engine.Run(t.Context(), realOnly)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal from the JSON-called child", err)
	}

	if !strings.Contains(err.Error(), "provisioner") {
		t.Fatalf("the refusal does not name the child's provisioner: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the child's provisioner executed despite the refusals")
	}
}

// TestAnUnmodelledJSONRunArgumentRetainsTheFloor: run arguments beyond
// `command` — expect_failures here — change what an execution outcome means,
// so their presence leaves the file unread and both gates closed.
func TestAnUnmodelledJSONRunArgumentRetainsTheFloor(t *testing.T) {
	t.Parallel()

	_, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "json-run-unmodelled")))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a floor refusal for the unmodelled run argument", err)
	}

	if !strings.Contains(err.Error(), "expect_failures") {
		t.Fatalf("the refusal does not name what it could not model: %v", err)
	}
}

// TestAnUnmodelledNestedTerraformConstructRetainsTheFloor: required_version is
// deliberately accepted (it can inform no gate); everything else inside the
// terraform block is refused rather than silently dropped.
func TestAnUnmodelledNestedTerraformConstructRetainsTheFloor(t *testing.T) {
	t.Parallel()

	_, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "json-terraform-unmodelled")))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a floor refusal for the unmodelled nested construct", err)
	}

	if !strings.Contains(err.Error(), "experiments") {
		t.Fatalf("the refusal does not name what it could not model: %v", err)
	}

	if strings.Contains(err.Error(), "required_version") {
		t.Fatalf("the deliberately accepted attribute is named as unmodelled: %v", err)
	}
}
