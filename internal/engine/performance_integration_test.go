//go:build integration

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The milestone's second exit gate: the full loop measured against a
// fully-mocked module backed by a realistically sized provider schema. It needs
// the network, because hashicorp/aws is not in the repository's offline mirror,
// so it lives behind the integration tag and the explicit opt-in.

const (
	allowRealInfrastructure = "TF_MUT_ALLOW_REAL_INFRASTRUCTURE"
	measureScaling          = "TF_MUT_MEASURE_SCALING"
	measurementJobs         = 8

	// awsMockedFixture is the realistically-sized provider fixture every
	// network-gated measurement uses.
	awsMockedFixture = "aws-mocked"
)

// measurement is the published shape of the exit-gate numbers.
type measurement struct {
	Fixture          string  `json:"fixture"`
	TerraformVersion string  `json:"terraform_version"`
	Cores            int     `json:"cores"`
	Jobs             int     `json:"jobs"`
	Mutants          int     `json:"mutants"`
	Executed         int     `json:"executed"`
	BaselineMS       int64   `json:"baseline_ms"`
	TotalSeconds     float64 `json:"total_seconds"`
	MutantsPerSecond float64 `json:"mutants_per_second"`
	MutationScore    float64 `json:"mutation_score"`
	AssertionScore   float64 `json:"assertion_score"`
	Reachability     float64 `json:"reachability"`
	PseudoTested     int     `json:"pseudo_tested"`
	// SequentialSeconds is the same population at one job, recorded only when
	// the scaling sweep is asked for.
	SequentialSeconds float64 `json:"sequential_seconds,omitempty"`
	Speedup           float64 `json:"speedup,omitempty"`
}

// It does not run in parallel: it is a wall-clock measurement, and a
// concurrent test would be measuring the other test.
//
//nolint:paralleltest // a timing measurement cannot share the machine.
func TestPerformanceAgainstAMockedRealProvider(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)

	config := networkConfig(t, module)
	config.Jobs = measurementJobs

	started := time.Now()

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	elapsed := time.Since(started)

	executed := 0

	for _, mutant := range result.Mutants {
		if mutant.State != report.NoCoverage && mutant.State != report.Pending {
			executed++
		}
	}

	if executed == 0 {
		t.Fatal("the measurement executed no mutants")
	}

	recorded := measurement{
		Fixture:          awsMockedFixture,
		TerraformVersion: result.TerraformVersion,
		Cores:            runtime.NumCPU(),
		Jobs:             config.Jobs,
		Mutants:          len(result.Mutants),
		Executed:         executed,
		BaselineMS:       result.Baseline.DurationMS,
		TotalSeconds:     elapsed.Seconds(),
		MutantsPerSecond: float64(executed) / elapsed.Seconds(),
		MutationScore:    result.Metrics.MutationScore,
		AssertionScore:   result.Metrics.AssertionScore,
		Reachability:     result.Metrics.Reachability,
		PseudoTested:     len(result.Findings),
	}

	if os.Getenv(measureScaling) == "1" {
		sequential := timeAtJobs(t, module, 1)
		recorded.SequentialSeconds = sequential.Seconds()
		recorded.Speedup = sequential.Seconds() / recorded.TotalSeconds
	}

	publish(t, recorded)

	t.Logf("%d mutants in %.1fs (%.2f mutants/s) at %d jobs",
		recorded.Executed, recorded.TotalSeconds, recorded.MutantsPerSecond, recorded.Jobs)
}

// BenchmarkPerformanceMockedRealProvider is the entry point the repository's
// performance recipe drives.
func BenchmarkPerformanceMockedRealProvider(b *testing.B) {
	if os.Getenv(allowRealInfrastructure) != "1" {
		b.Skipf("%s=1 is required for the real-provider measurement", allowRealInfrastructure)
	}

	module := benchmarkFixture(b)

	b.ResetTimer()

	for range b.N {
		config := engine.Config{ //nolint:exhaustruct // defaults are the point of the measurement.
			ModuleDir: module,
			Jobs:      measurementJobs,
			Env:       []string{checkpointDisabled, inAutomation},
			WorkDir:   b.TempDir(),
		}

		if _, err := engine.Run(b.Context(), config); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}

// timeAtJobs re-runs the same population at a different parallelism level, so
// the wall-clock claim rests on a measurement rather than on an assumption.
func timeAtJobs(t *testing.T, module string, jobs int) time.Duration {
	t.Helper()

	config := networkConfig(t, module)
	config.Jobs = jobs

	started := time.Now()

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("run at %d jobs: %v", jobs, err)
	}

	return time.Since(started)
}

func requireRealInfrastructureOptIn(t *testing.T) {
	t.Helper()

	if os.Getenv(allowRealInfrastructure) != "1" {
		t.Skipf("%s=1 is required for the real-provider measurement", allowRealInfrastructure)
	}
}

// networkConfig deliberately omits the repository's offline CLI configuration:
// the mirror carries hashicorp/null only, and this fixture needs the registry.
func networkConfig(t *testing.T, module string) engine.Config {
	t.Helper()

	config := baseConfig(t, module)
	config.Env = []string{
		checkpointDisabled,
		inAutomation,
		"TF_PLUGIN_CACHE_DIR=" + pluginCache(t),
	}

	return config
}

// pluginCache keeps the multi-hundred-megabyte provider between runs.
func pluginCache(t *testing.T) string {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		return t.TempDir()
	}

	cache := filepath.Join(root, ".artifacts", "cache", "terraform-plugins")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatalf("creating plugin cache: %v", err)
	}

	return cache
}

func publish(t *testing.T, recorded measurement) {
	t.Helper()

	root, found := repositoryRoot(t)
	if !found {
		return
	}

	directory := filepath.Join(root, ".artifacts", "performance")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("creating %s: %v", directory, err)
	}

	encoded, err := json.MarshalIndent(recorded, "", "  ")
	if err != nil {
		t.Fatalf("encoding measurement: %v", err)
	}

	path := filepath.Join(directory, "m1-exit-gate.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func benchmarkFixture(b *testing.B) string {
	b.Helper()

	target := filepath.Join(b.TempDir(), awsMockedFixture)
	source := filepath.Join(fixtureRoot, awsMockedFixture)

	if err := os.MkdirAll(filepath.Join(target, "tests"), 0o750); err != nil {
		b.Fatalf("creating fixture: %v", err)
	}

	for _, name := range []string{mainFile, filepath.Join("tests", "unit.tftest.hcl")} {
		//nolint:gosec // a repository-owned fixture path.
		content, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			b.Fatalf("reading fixture: %v", err)
		}

		//nolint:gosec // the destination is inside b.TempDir().
		if err := os.WriteFile(filepath.Join(target, name), content, 0o600); err != nil {
			b.Fatalf("writing fixture: %v", err)
		}
	}

	return target
}
