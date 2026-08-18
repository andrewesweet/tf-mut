package engine_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M4.0 floor set. Every case here is one of the seven the C1 disposition
// named verbatim — "JSON-only unmocked provider; JSON-only provisioner; JSON
// test mock status; JSON auto-var changing `count`; malformed JSON; configured
// exclusions hiding none of these; zero Terraform runs before refusal" — plus
// issue #57's two original reproductions.
//
// The claim under test is what happens while the content is *unread*, so every
// case here runs under the `DisableJSONReading` seam control, over the same
// fixtures whose content the M4c slice reads in `jsonslice_test.go`. The two
// files together are the floor and its lift: `floorConfig` proves the refusal
// is decided from unreadness, and the slice proves it is decided from content.
//
// The claim under test is a refusal, so these run without the provider mirror:
// nothing executes.

// floorConfig is a run whose JSON content is deliberately left unread.
func floorConfig(t *testing.T, fixture string) engine.Config {
	t.Helper()

	config := baseConfig(t, copyFixture(t, fixture))
	config.DisableJSONReading = true

	return config
}

const (
	jsonProviderFixture    = "json-provider"
	jsonProvisionerFixture = "json-provisioner"
	jsonTestMockFixture    = "json-test-mock"
	jsonAutoVarFixture     = "json-autovar"
	jsonMalformedFixture   = "json-malformed"
	jsonMixedFixture       = "json-mixed"
	jsonProvidersFileName  = "providers.tf.json"
	jsonEffectsFileName    = "effects.tf.json"
	jsonTestFileName       = "tests/unit.tftest.json"
	jsonMalformedFileName  = "broken.tf.json"
	jsonAutoVarFileName    = "terraform.tfvars.json"
	realInfrastructureFlag = "--allow-real-infrastructure"
	unsandboxedEffectsFlag = "--allow-unsandboxed-effects"
)

func TestUnreadJSONFailsTheRealInfrastructureGateClosed(t *testing.T) {
	t.Parallel()

	for _, fixture := range []struct {
		name string
		file string
	}{
		{name: jsonProviderFixture, file: jsonProvidersFileName},
		{name: jsonTestMockFixture, file: jsonTestFileName},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()

			_, err := engine.Run(t.Context(), floorConfig(t, fixture.name))
			if !errors.Is(err, engine.ErrRealInfrastructure) {
				t.Fatalf("error = %v, want a real-infrastructure refusal", err)
			}

			assertRefusalNames(t, err, fixture.file, realInfrastructureFlag)
		})
	}
}

func TestUnreadJSONFailsTheUnsandboxedEffectsGateClosed(t *testing.T) {
	t.Parallel()

	config := floorConfig(t, jsonProvisionerFixture)
	// Authorising one gate must never lift the other: the two flags authorise
	// different risks and the unread file could carry either.
	config.AllowRealInfrastructure = true

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal", err)
	}

	assertRefusalNames(t, err, jsonEffectsFileName, unsandboxedEffectsFlag)
}

func TestEachSafetyGateFailsClosedIndependentlyOnUnreadJSON(t *testing.T) {
	t.Parallel()

	// Neither flag: the first gate the unread content could have informed
	// refuses.
	noFlags := floorConfig(t, jsonProvisionerFixture)
	if _, err := engine.Run(t.Context(), noFlags); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("with no flag, error = %v, want a real-infrastructure refusal", err)
	}

	// The effects flag alone leaves the real-infrastructure gate closed.
	effectsOnly := floorConfig(t, jsonProvisionerFixture)
	effectsOnly.AllowUnsandboxedEffects = true

	if _, err := engine.Run(t.Context(), effectsOnly); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("with only the effects flag, error = %v, want a real-infrastructure refusal", err)
	}
}

func TestMalformedJSONRetainsTheFloorRatherThanLiftingIt(t *testing.T) {
	t.Parallel()

	// No seam control: this file is genuinely unreadable, which is the point.
	_, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, jsonMalformedFixture)))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a refusal: a parse failure is never permission", err)
	}

	assertRefusalNames(t, err, jsonMalformedFileName, realInfrastructureFlag)
}

func TestConfiguredExclusionsCannotHideUnreadJSON(t *testing.T) {
	t.Parallel()

	for _, exclusion := range []string{"*.json", "*", "effects.tf.json"} {
		t.Run(exclusion, func(t *testing.T) {
			t.Parallel()

			config := floorConfig(t, jsonProvisionerFixture)
			config.ExcludePaths = []string{exclusion}

			_, err := engine.Run(t.Context(), config)
			if !errors.Is(err, engine.ErrRealInfrastructure) {
				t.Fatalf("exclusion %q hid the floor: error = %v", exclusion, err)
			}
		})
	}
}

func TestNoTerraformRunPrecedesAFloorRefusal(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "terraform-calls")

	config := floorConfig(t, jsonProvisionerFixture)
	config.TerraformBinary = recordingTerraform(t, log)

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a refusal", err)
	}

	for _, invocation := range terraformInvocations(t, log) {
		if invocation != "version" {
			t.Fatalf("terraform %s ran before the refusal; a refusal must be free", invocation)
		}
	}
}

// TestAJSONAutoVariableFileKeepsTheStaticShortcutsDown is the auto-var case.
// The variables classes inform neither safety gate — they declare no provider,
// no provisioner and no run — so the run proceeds; what they do inform is the
// static evaluation, and the floor withdraws every claim that depended on it.
func TestAJSONAutoVariableFileKeepsTheStaticShortcutsDown(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), floorConfig(t, jsonAutoVarFixture))
	if err != nil {
		t.Fatalf("a JSON variables file informs no safety gate and must not refuse: %v", err)
	}

	assertDegradationWarned(t, result, jsonAutoVarFileName)

	for _, mutant := range result.Mutants {
		if mutant.State == report.NoCoverage {
			t.Fatalf("mutant %s at %s was pre-classified NoCoverage over an unread variables file",
				mutant.ID, mutant.Site)
		}
	}
}

// TestUnreadJSONDisablesEveryStaticShortcut is issue #57's second reproduction.
//
// `local.json_only` is read only by a JSON-declared resource, so the `.tf`-only
// reference graph gives it an empty forward cone and the static `Unobservable`
// shortcut would classify its mutants without executing them — a false proof of
// a value the suite demonstrably observes. Under the floor the shortcut is
// withdrawn and the executed verdict stands: the assertion on `output.side`
// catches the mutant.
func TestUnreadJSONDisablesEveryStaticShortcut(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	config := floorConfig(t, jsonMixedFixture)
	config.AllowRealInfrastructure = true
	config.AllowUnsandboxedEffects = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertDegradationWarned(t, result, "side.tf.json")

	if state := stateOf(t, result, "local.json_only"); state != report.Killed {
		t.Fatalf("local.json_only mutant = %s, want Killed: the static shortcut proved "+
			"unobservability over a graph that never saw the JSON reader", state)
	}
}

// TestTheHCLOnlyGraphIsTheFalseProofTheFloorWithdraws states the fail-open edge
// the test above repairs, over the adapters directly (the M3 seam exception):
// without the floor, `local.json_only` has an empty observable cone, and the
// static shortcut would classify a demonstrably observable value `Unobservable`.
func TestTheHCLOnlyGraphIsTheFalseProofTheFloorWithdraws(t *testing.T) {
	t.Parallel()

	configuration, err := discovery.DiscoverWith(
		filepath.Join(fixtureRoot, jsonMixedFixture), engine.DefaultTestDirectory,
		discovery.Options{SkipJSON: true})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	cone, mapped := configuration.BuildGraph().SiteCone(".", "local.json_only")
	if !mapped {
		t.Fatal("local.json_only does not map into the graph at all")
	}

	if cone.ContainsObservable() {
		t.Skip("the graph already reads the JSON reader: the false proof is gone")
	}

	if len(configuration.UnreadJSON()) == 0 {
		t.Fatal("the graph is missing the JSON edge and the floor is not down for it")
	}
}

func TestAuthorisingBothGatesStillReportsTheUnreadJSON(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	config := floorConfig(t, jsonMixedFixture)
	config.AllowRealInfrastructure = true
	config.AllowUnsandboxedEffects = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	joined := strings.Join(result.Warnings, "\n")

	for _, flag := range []string{realInfrastructureFlag, unsandboxedEffectsFlag} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("accepting %s over unread JSON was not reported: %v", flag, result.Warnings)
		}
	}
}

func TestAPreviewIsNeverRefusedByTheFloor(t *testing.T) {
	t.Parallel()

	config := floorConfig(t, jsonProvisionerFixture)
	config.Preview = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("a preview executes nothing and must not be refused: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("preview generated no mutants")
	}
}

func TestNoJSONInTheClosureLeavesTheFloorUp(t *testing.T) {
	t.Parallel()

	result := runFixture(t, "skeleton")

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "unread JSON") {
			t.Fatalf("a closure with no JSON reported a floor: %q", warning)
		}
	}
}

func assertRefusalNames(t *testing.T, err error, file, flag string) {
	t.Helper()

	if !strings.Contains(err.Error(), filepath.Base(file)) {
		t.Fatalf("refusal does not name the unread file %s: %v", file, err)
	}

	if !strings.Contains(err.Error(), flag) {
		t.Fatalf("refusal does not name %s, the flag whose risk the unread content carries: %v", flag, err)
	}
}

func assertDegradationWarned(t *testing.T, result report.Report, file string) {
	t.Helper()

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "unread JSON") && strings.Contains(warning, file) {
			return
		}
	}

	t.Fatalf("the run did not report the withdrawn static claims for %s: %v", file, result.Warnings)
}

// recordingTerraform wraps the real binary in a script that appends each
// subcommand to a log, so a test can prove what did and did not execute.
func recordingTerraform(t *testing.T, log string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "terraform-recording")
	script := "#!/usr/bin/env bash\n" +
		"if [ \"${1:-}\" = \"--tf-mut-probe\" ]; then exit 0; fi\n" +
		"for argument in \"$@\"; do\n" +
		"  case \"$argument\" in\n" +
		"    -*) continue ;;\n" +
		"    *) printf '%s\\n' \"$argument\" >>\"" + log + "\"; break ;;\n" +
		"  esac\n" +
		"done\n" +
		"exec terraform \"$@\"\n"

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // the wrapper must be executable.
		t.Fatalf("writing the recording terraform wrapper: %v", err)
	}

	// Linux reports ETXTBSY for a freshly written executable while any process
	// in this test binary is between fork and exec, and these tests run in
	// parallel. Probe until the kernel lets the wrapper run; the probe argument
	// records nothing and executes nothing.
	for range probeAttempts {
		//nolint:gosec // a test-owned wrapper script.
		if err := exec.CommandContext(t.Context(), path, "--tf-mut-probe").Run(); err == nil {
			return path
		}

		time.Sleep(probeInterval)
	}

	t.Fatal("the recording terraform wrapper never became executable")

	return ""
}

// The bounds of the executable probe above.
const (
	probeAttempts = 200
	probeInterval = 10 * time.Millisecond
)

func terraformInvocations(t *testing.T, log string) []string {
	t.Helper()

	content, err := os.ReadFile(log) //nolint:gosec // test-owned log path.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		t.Fatalf("reading %s: %v", log, err)
	}

	lines := []string{}

	for line := range strings.SplitSeq(strings.TrimSpace(string(content)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}
