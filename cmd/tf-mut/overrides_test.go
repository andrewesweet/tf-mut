package main

import (
	"bytes"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The flag-to-override mapping is the whole translation between the command
// line's vocabulary and the engine's setting names: a flag the caller set
// explicitly becomes an override of one engine setting, keyed by that
// setting's name. The expected vocabulary is spelled here, not read back from
// the production table, so a dropped or mistyped entry turns this red. Each
// entry is parsed on its own, and the assertion counts the set, so a second
// leaked entry fails alongside a missing one.
func TestEveryOverrideFlagMapsOntoExactlyItsEngineSetting(t *testing.T) {
	t.Parallel()

	expected := map[string]engine.Setting{
		testDirectoryFlag:    engine.SettingTestDirectory,
		jobsFlag:             engine.SettingJobs,
		timeoutFactorFlag:    engine.SettingTimeoutFactor,
		preflightTimeoutFlag: engine.SettingPreflightTimeout,
		minScoreFlag:         engine.SettingMinScore,
		allowIncompleteFlag:  engine.SettingAllowIncompleteScore,
		tierFlag:             engine.SettingTier,
		reporterFlagName:     engine.SettingReporter,
	}

	if !maps.Equal(overrideOf, expected) {
		t.Fatalf("the flag-to-override mapping drifted:%s", diffKeys(overrideOf, expected))
	}

	for flagName, setting := range expected {
		t.Run(flagName, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"--" + flagName}, overrideFlagValue(flagName)...)
			parsed, err := parse(runCommand, "test", append(args, "."), &bytes.Buffer{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			request, ok := parsed.config.(engine.RunRequest)
			if !ok {
				t.Fatalf("request is %T, want engine.RunRequest", parsed.config)
			}

			if len(request.Overrides) != 1 {
				t.Fatalf("overrides = %v, want exactly %q", request.Overrides, setting)
			}

			if !request.Overrides[setting] {
				t.Fatalf("--%s did not override setting %q", flagName, setting)
			}
		})
	}
}

// diffKeys names the entries that differ between the production mapping and
// the expected vocabulary, so a drift is reported by name.
func diffKeys(got, want map[string]engine.Setting) string {
	var drift strings.Builder

	for flagName, setting := range want {
		if gotSetting, present := got[flagName]; !present {
			fmt.Fprintf(&drift, "\n  --%s: missing, want %q", flagName, setting)
		} else if gotSetting != setting {
			fmt.Fprintf(&drift, "\n  --%s: %q, want %q", flagName, gotSetting, setting)
		}
	}

	for flagName, setting := range got {
		if _, present := want[flagName]; !present {
			fmt.Fprintf(&drift, "\n  --%s: unexpected override of %q", flagName, setting)
		}
	}

	return drift.String()
}

// overrideFlagValue is one syntactically valid value per flag, so the mapping
// test parses every entry the same way. Boolean flags take no value.
func overrideFlagValue(flagName string) []string {
	if flagName == allowIncompleteFlag {
		return nil
	}

	values := map[string]string{
		testDirectoryFlag:    "tests",
		jobsFlag:             "1",
		timeoutFactorFlag:    "2",
		preflightTimeoutFlag: "1m",
		minScoreFlag:         "70",
		tierFlag:             "smoke",
		reporterFlagName:     reporterJSON,
	}

	return []string{values[flagName]}
}

// A flag the caller never set must record no override at all: an unset flag
// leaves the configured scalar in force.
func TestAnUnsetFlagRecordsNoOverride(t *testing.T) {
	t.Parallel()

	parsed, err := parse(runCommand, "test", []string{"."}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	request, ok := parsed.config.(engine.RunRequest)
	if !ok {
		t.Fatalf("request is %T, want engine.RunRequest", parsed.config)
	}

	if len(request.Overrides) != 0 {
		t.Fatalf("overrides = %v, want none", request.Overrides)
	}
}

// The engine reports refusal reasons; the flags that remedy them are the
// command line's to spell. Each rendered refusal must carry its flag text, at
// the position the published wording puts it.
func TestDeliveryRefusalsSpellTheirFlags(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "since ref",
			err:  fmt.Errorf("%w: %q (unknown ref)", engine.ErrSinceRef, "main"),
			want: `--since ref cannot be resolved: "main" (unknown ref)`,
		},
		{
			name: "since repository",
			err:  fmt.Errorf("%w: /mod is not inside a git work tree", engine.ErrSinceRepository),
			want: "--since requires a git repository: /mod is not inside a git work tree",
		},
		{
			name: "since conflict",
			err:  engine.ErrSinceConflict,
			want: "--since cannot select over an unresolved merge conflict",
		},
		{
			name: "sample range",
			err:  fmt.Errorf("%w: %g", engine.ErrSampleRange, 150.0),
			want: "--sample must be a percentage greater than 0 and at most 100: 150",
		},
		{
			name: "sampled gate",
			err:  engine.ErrSampledGate,
			want: "a sampled run is non-authoritative and cannot satisfy a gate without --allow-sampled-gate",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			if got := renderError(refusal.err); got != refusal.want {
				t.Fatalf("renderError = %q, want %q", got, refusal.want)
			}
		})
	}
}

// An error that carries no delivery vocabulary renders unchanged.
func TestAnUnrelatedErrorRendersUnchanged(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("%w: provider aws is not mocked", engine.ErrRealInfrastructure)
	if got, want := renderError(err), err.Error(); got != want {
		t.Fatalf("renderError = %q, want %q", got, want)
	}

	if strings.Contains(renderError(err), "--since") {
		t.Fatal("an unrelated refusal must not gain a since prefix")
	}
}

// The sampled-gate refusal reaches the user through the whole command: the
// stderr names the opt-in flag, whose spelling the engine no longer carries.
func TestTheSampledGateRefusalNamesTheOptInFlag(t *testing.T) {
	t.Parallel()

	module := fixture(t)

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	code := run([]string{runCommand, "--sample", "50", "--" + minScoreFlag, "70", module}, "dev", &stdout, &stderr)
	if code != report.ExitOperational {
		t.Fatalf("exit code = %d, want %d", code, report.ExitOperational)
	}

	if !strings.Contains(stderr.String(), "--allow-sampled-gate") {
		t.Fatalf("the refusal does not spell the opt-in flag: %s", stderr.String())
	}
}
