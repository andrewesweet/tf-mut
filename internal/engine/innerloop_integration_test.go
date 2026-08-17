//go:build integration

package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3c.1 (#50): the real-provider gate, under the pinned protocol from the M5
// disposition. The fixture is pinned by content (the repository commit is the
// pin), the diff is exactly one appended comment line in observability.tf,
// the expected selected sites are enumerated with a non-zero minimum, the
// cache is off, the tier is standard, and the protocol is warm: the plugin
// cache directory persists between runs, and the full run precedes the
// scoped one. The portable regression assertion is the selected count plus
// the factor versus the full-run baseline; the wall-clock figure is a
// published measurement on named hardware, never a portable assertion.

// innerLoopDiff is the pinned diff: a comment, so every mutant identifier in
// the touched file survives unchanged.
const innerLoopDiff = "\n# inner-loop probe: the pinned one-file diff (M3c, issue #50)\n"

// expectedSelectedSites enumerates the pinned diff's selection: every mutant
// in observability.tf — the log group resource — and nothing else.
func expectedSelectedSites() []string {
	return []string{
		"aws_cloudwatch_log_group.application",
		"aws_cloudwatch_log_group.application.name",
		"aws_cloudwatch_log_group.application.retention_in_days",
		"aws_cloudwatch_log_group.application.tags",
	}
}

// expectedSelectedIDs enumerates the pinned diff's twelve mutant identifiers
// (M5: "expected selected mutant IDs enumerated with a non-zero minimum").
// Identifiers are content-derived, so this list moves only when the pinned
// fixture or the operator catalogue does — which is exactly what the pin is
// for.
func expectedSelectedIDs() []string {
	return []string{
		"1a1018166288", "1b2dbea0dabe", "23142f1639df", "573d08a7c44e",
		"6ced0d93372f", "8b506b269716", "afa0feaca4bc", "bc7c1b5a9868",
		"dbd5e75e01ad", "e0f249e966dd", "e1598a8345a0", "e9aa8f23bea3",
	}
}

// innerLoopMeasurement is the published shape of the M3c numbers.
type innerLoopMeasurement struct {
	Fixture           string   `json:"fixture"`
	TerraformVersion  string   `json:"terraform_version"`
	Cores             int      `json:"cores"`
	Jobs              int      `json:"jobs"`
	Tier              string   `json:"tier"`
	CacheState        string   `json:"cache_state"`
	WarmCold          string   `json:"warm_cold"`
	FullMutants       int      `json:"full_mutants"`
	FullSeconds       float64  `json:"full_seconds"`
	SelectedMutants   int      `json:"selected_mutants"`
	SelectedSites     []string `json:"selected_sites"`
	ScopedSeconds     float64  `json:"scoped_seconds"`
	Factor            float64  `json:"factor"`
	SurvivorCount     int      `json:"survivor_count"`
	SurvivorDeltas    []int    `json:"survivor_delta_sizes"`
	DynamicZeroState  string   `json:"dynamic_zero_state"`
	MockMaskedOutcome string   `json:"mock_masked_outcome"`
}

// It is a wall-clock measurement; nothing else may share the machine.
//
//nolint:paralleltest // a timing measurement cannot share the machine.
func TestInnerLoopGateAgainstTheRealProvider(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, awsMockedFixture)
	git(t, module, "init", "--quiet", "--initial-branch=main")
	git(t, module, "add", "--all")
	commit(t, module, "pinned fixture")

	// Full population, cache off, standard tier.
	fullConfig := networkConfig(t, module)
	fullConfig.Jobs = measurementJobs
	fullConfig.NoCache = true

	fullStart := time.Now()

	full, err := engine.Run(t.Context(), fullConfig)
	if err != nil {
		t.Fatalf("full run: %v", err)
	}

	fullSeconds := time.Since(fullStart).Seconds()

	// The pinned diff.
	appendFile(t, filepath.Join(module, "observability.tf"), innerLoopDiff)

	scopedConfig := networkConfig(t, module)
	scopedConfig.Jobs = measurementJobs
	scopedConfig.NoCache = true
	scopedConfig.Since = "HEAD"

	scopedStart := time.Now()

	scoped, err := engine.Run(t.Context(), scopedConfig)
	if err != nil {
		t.Fatalf("scoped run: %v", err)
	}

	scopedSeconds := time.Since(scopedStart).Seconds()

	// The portable regression assertions: the enumerated selection, with a
	// non-zero minimum, and the factor against the full-run baseline.
	sites := assertPinnedSelection(t, scoped)
	assertVerdictInvariance(t, full, scoped)

	factor := fullSeconds / scopedSeconds
	if factor <= 1 {
		t.Fatalf("the scoped run (%.1fs) was not faster than the full run (%.1fs)",
			scopedSeconds, fullSeconds)
	}

	// DYNAMIC-ZERO's first end-to-end classification: the dynamic block's
	// for_each empties, the attribute disappears, and the length assertion
	// kills it.
	dynamicState := stateOfOperator(t, full, "DYNAMIC-ZERO")
	if dynamicState != report.Killed {
		t.Fatalf("DYNAMIC-ZERO classified %s end-to-end, want %s", dynamicState, report.Killed)
	}

	deltas := survivorDeltaSizes(full)

	publishInnerLoop(t, innerLoopMeasurement{
		Fixture:           awsMockedFixture,
		TerraformVersion:  full.TerraformVersion,
		Cores:             runtime.NumCPU(),
		Jobs:              measurementJobs,
		Tier:              "standard",
		CacheState:        "off (--no-cache)",
		WarmCold:          "warm plugin cache; full run precedes scoped run",
		FullMutants:       len(full.Mutants),
		FullSeconds:       fullSeconds,
		SelectedMutants:   len(scoped.Mutants),
		SelectedSites:     sites,
		ScopedSeconds:     scopedSeconds,
		Factor:            factor,
		SurvivorCount:     len(full.Survivors()),
		SurvivorDeltas:    deltas,
		DynamicZeroState:  string(dynamicState),
		MockMaskedOutcome: "withdrawn: positive case cannot fire (see aws-applied refutation)",
	})

	t.Logf("full %d mutants in %.1fs; scoped %d in %.1fs; factor %.1fx",
		len(full.Mutants), fullSeconds, len(scoped.Mutants), scopedSeconds, factor)
}

// TestTheMockMaskedRefutationHolds pins the withdrawal's evidence on the real
// provider: an apply-mode delta confined to an optional-computed attribute is
// attributable to the module, and the survivor carries an actionable closure
// diagnosis — never an indeterminate one, and never the withdrawn diagnosis.
//
//nolint:paralleltest // shares the integration plugin cache serially.
func TestTheMockMaskedRefutationHolds(t *testing.T) {
	requireRealInfrastructureOptIn(t)

	module := copyFixture(t, "aws-applied")

	config := networkConfig(t, module)
	config.NoCache = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	site := "aws_sqs_queue.work.kms_data_key_reuse_period_seconds"
	found := false

	for _, mutant := range result.Mutants {
		if mutant.Site != site || mutant.Operator != "EXT-ATTR-DELETE" {
			continue
		}

		found = true

		if mutant.State != report.Survived {
			t.Fatalf("the refutation mutant classified %s, want %s", mutant.State, report.Survived)
		}

		if mutant.Verdict == nil || !mutant.Verdict.Diagnosis.Actionable() {
			t.Fatalf("the stable computed-flagged delta was not attributed to the module: %+v",
				mutant.Verdict)
		}
	}

	if !found {
		t.Fatal("the refutation site generated no EXT-ATTR-DELETE mutant")
	}
}

// assertPinnedSelection holds the gate's enumerated pins: the four sites and
// the twelve mutant identifiers, with a non-zero minimum and a non-empty
// omission.
func assertPinnedSelection(t *testing.T, scoped report.Report) []string {
	t.Helper()

	sites := selectedSites(scoped)
	if len(scoped.Mutants) == 0 {
		t.Fatal("the pinned diff selected nothing; an empty selection fails the gate")
	}

	if !slices.Equal(sites, expectedSelectedSites()) {
		t.Fatalf("the pinned diff selected %v, expected %v", sites, expectedSelectedSites())
	}

	ids := selectedIDs(scoped)
	if !slices.Equal(ids, expectedSelectedIDs()) {
		t.Fatalf("the pinned diff selected IDs %v, expected %v", ids, expectedSelectedIDs())
	}

	if scoped.Population.Omitted == 0 {
		t.Fatal("the scoped run omitted nothing; the lever did not engage")
	}

	return sites
}

func selectedIDs(result report.Report) []string {
	ids := make([]string, 0, len(result.Mutants))
	for _, mutant := range result.Mutants {
		ids = append(ids, mutant.ID)
	}

	slices.Sort(ids)

	return ids
}

func selectedSites(result report.Report) []string {
	sites := []string{}

	for _, mutant := range result.Mutants {
		if !slices.Contains(sites, mutant.Site) {
			sites = append(sites, mutant.Site)
		}
	}

	slices.Sort(sites)

	return sites
}

func stateOfOperator(t *testing.T, result report.Report, operator string) report.State {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.Operator == operator {
			return mutant.State
		}
	}

	t.Fatalf("no %s mutant in the population", operator)

	return ""
}

func survivorDeltaSizes(result report.Report) []int {
	sizes := []int{}

	for _, mutant := range result.Survivors() {
		if mutant.Verdict == nil {
			continue
		}

		sizes = append(sizes, len(mutant.Verdict.Evidence.Delta))
	}

	slices.Sort(sizes)

	return sizes
}

func publishInnerLoop(t *testing.T, recorded innerLoopMeasurement) {
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

	path := filepath.Join(directory, "m3-inner-loop.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
