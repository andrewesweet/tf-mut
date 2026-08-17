package engine_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3b.3 (#49): the baseline file under the normative gate truth table (C8).
// Staleness and baseline rewrite require a full unsampled population; a
// baseline entry absent from a scoped population is unobserved, not stale;
// any current finding whose stable ID and actionability class are not
// accepted is new — a previously-Killed ID surviving is new, an accepted
// indeterminate turning actionable is new.

// weakenTests replaces the fixture's assertion so its killed mutants regress
// to survival without changing any mutant identifier: identifiers derive from
// module sources, and only the test file changes.
func weakenTests(t *testing.T, module string) {
	t.Helper()

	path := filepath.Join(module, "tests", "unit.tftest.hcl")

	writeFile(t, path, `run "weak" {
  command = plan

  assert {
    condition     = output.tier != "impossible"
    error_message = "cannot fail"
  }
}
`)
}

// failOnNewGate is the gate under test everywhere in this file.
func failOnNewGate() report.Gate {
	return report.Gate{
		MinScore: 0, HasMinScore: false, AllowIncompleteScore: false, FailOnNew: true,
	}
}

func runWith(t *testing.T, config engine.Config) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	return result
}

func findingIDs(result report.Report) []string {
	ids := []string{}

	for _, mutant := range result.Mutants {
		if mutant.State == report.Survived || mutant.State == report.StructurallyUnassertable {
			ids = append(ids, mutant.ID)
		}
	}

	slices.Sort(ids)

	return ids
}

// TestAdoptionThenRegression is the demo and three rows at once: a baseline
// written on a full run accepts today's findings, CI passes on them, and a
// previously-Killed mutant regressing to survival is new.
func TestAdoptionThenRegression(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	// Adopt: everything is killed today, so the baseline is empty and the
	// write is permitted on a full population.
	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true

	adopted := runWith(t, adopt)

	if adopted.Gates == nil || adopted.Gates.Baseline == nil ||
		adopted.Gates.Baseline.Write != report.BaselineWritten {
		t.Fatalf("the full-run baseline write was not recorded: %+v", adopted.Gates)
	}

	// The gate passes against the fresh baseline.
	check := baseConfig(t, module)
	check.FailOnNew = true

	passing := runWith(t, check)
	if code := passing.ExitCode(failOnNewGate()); code != report.ExitClean {
		t.Fatalf("a clean run against a fresh baseline exits %d, want %d", code, report.ExitClean)
	}

	// Regress: weaken the suite so the killed mutants survive. Identifiers
	// are unchanged — a previously-Killed ID now surviving is new.
	weakenTests(t, module)

	regressed := runWith(t, check)

	if regressed.Gates == nil || regressed.Gates.Baseline == nil ||
		len(regressed.Gates.Baseline.New) == 0 {
		t.Fatal("killed-regressed-to-survived produced no new findings")
	}

	if code := regressed.ExitCode(failOnNewGate()); code != report.ExitFindings {
		t.Fatalf("a regression exits %d, want %d", code, report.ExitFindings)
	}
}

// TestAcceptedSurvivorsPassAndStayInScores: the baseline accepts the current
// findings, the gate passes, the survivors keep their states and scores, and
// each carries the accepted label.
func TestAcceptedSurvivorsPassAndStayInScores(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	weakenTests(t, module)

	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true

	adopted := runWith(t, adopt)

	survivors := findingIDs(adopted)
	if len(survivors) == 0 {
		t.Fatal("the weakened fixture produced no findings to accept")
	}

	check := baseConfig(t, module)
	check.FailOnNew = true

	accepted := runWith(t, check)

	if code := accepted.ExitCode(failOnNewGate()); code != report.ExitClean {
		t.Fatalf("accepted findings exit %d, want %d", code, report.ExitClean)
	}

	if accepted.Metrics.Counts[report.Survived] == 0 {
		t.Fatal("accepted survivors vanished from the scored set")
	}

	for _, mutant := range accepted.Mutants {
		if mutant.State != report.Survived && mutant.State != report.StructurallyUnassertable {
			continue
		}

		if mutant.Provenance == nil || mutant.Provenance.BaselineStatus != "accepted" {
			t.Errorf("finding %s is not labelled accepted: %+v", mutant.ID, mutant.Provenance)
		}
	}
}

// TestAnAcceptedIndeterminateTurningActionableIsNew: acceptance is by stable
// ID and actionability class, so a diagnosis crossing the actionability line
// re-raises the finding.
func TestAnAcceptedIndeterminateTurningActionableIsNew(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	weakenTests(t, module)

	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true

	adopted := runWith(t, adopt)

	ids := findingIDs(adopted)
	if len(ids) == 0 {
		t.Fatal("no findings to rewrite")
	}

	// Rewrite the written baseline: same IDs, actionability downgraded, as if
	// the findings had been accepted while indeterminate.
	path := filepath.Join(module, engine.DefaultBaselineName)

	content, err := os.ReadFile(path) //nolint:gosec // test-owned fixture copy.
	if err != nil {
		t.Fatalf("reading baseline: %v", err)
	}

	document := map[string]any{}
	if decodeErr := json.Unmarshal(content, &document); decodeErr != nil {
		t.Fatalf("decoding baseline: %v", decodeErr)
	}

	entries, ok := document["entries"].([]any)
	if !ok {
		t.Fatal("the baseline document carries no entries list")
	}

	for _, raw := range entries {
		if entry, ok := raw.(map[string]any); ok {
			entry["actionable"] = false
		}
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding baseline: %v", err)
	}

	writeFile(t, path, string(encoded))

	check := baseConfig(t, module)
	check.FailOnNew = true

	result := runWith(t, check)

	if result.Gates == nil || result.Gates.Baseline == nil || len(result.Gates.Baseline.New) == 0 {
		t.Fatal("an accepted indeterminate turning actionable raised nothing new")
	}
}

// TestStaleAndUnobservedAreDistinguished: a full population reports an
// unmatched entry stale; a scoped population reports it unobserved and never
// stale.
func TestStaleAndUnobservedAreDistinguished(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "all-killed")

	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true
	runWith(t, adopt)

	// Plant an entry no run will ever match.
	path := filepath.Join(module, engine.DefaultBaselineName)

	content, err := os.ReadFile(path) //nolint:gosec // test-owned fixture copy.
	if err != nil {
		t.Fatalf("reading baseline: %v", err)
	}

	document := map[string]any{}
	if decodeErr := json.Unmarshal(content, &document); decodeErr != nil {
		t.Fatalf("decoding baseline: %v", decodeErr)
	}

	entries, ok := document["entries"].([]any)
	if !ok {
		t.Fatal("the baseline document carries no entries list")
	}

	entries = append(entries, map[string]any{"id": "feedfeedfeed", "actionable": true})
	document["entries"] = entries

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding baseline: %v", err)
	}

	writeFile(t, path, string(encoded))

	full := baseConfig(t, module)
	full.FailOnNew = true
	// The Full row is the full, unsampled, freshly executed population: the
	// cache would demote this run to the cached row.
	full.NoCache = true

	fullResult := runWith(t, full)

	if fullResult.Gates == nil || fullResult.Gates.Baseline == nil ||
		!slices.Contains(fullResult.Gates.Baseline.Stale, "feedfeedfeed") {
		t.Fatalf("a full population did not report the unmatched entry stale: %+v",
			fullResult.Gates.Baseline)
	}

	if !fullResult.Gates.Baseline.StalenessReported {
		t.Fatal("a full population must report staleness")
	}

	// Scope the run to nothing: touch no .tf file, select by HEAD.
	scoped := baseConfig(t, module)
	scoped.FailOnNew = true
	scoped.Since = sinceHead

	scopedResult := runWith(t, scoped)

	baseline := scopedResult.Gates.Baseline
	if baseline == nil {
		t.Fatal("the scoped run carries no baseline gate record")
	}

	if len(baseline.Stale) != 0 || baseline.StalenessReported {
		t.Fatalf("a scoped population reported staleness: %+v", baseline)
	}

	if !slices.Contains(baseline.Unobserved, "feedfeedfeed") {
		t.Fatalf("the unselected entry is not reported unobserved: %+v", baseline)
	}
}

// TestBaselineWriteIsRefusedOffTheFullPopulation: a scoped or sampled run can
// never silently shrink the accepted list.
func TestBaselineWriteIsRefusedOffTheFullPopulation(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "all-killed")

	scoped := baseConfig(t, module)
	scoped.WriteBaseline = true
	scoped.Since = sinceHead

	if _, err := engine.Run(t.Context(), scoped); !errors.Is(err, engine.ErrBaselineWrite) {
		t.Fatalf("a scoped baseline write returned %v, want ErrBaselineWrite", err)
	}

	sampled := baseConfig(t, module)
	sampled.WriteBaseline = true
	sampled.HasSample = true
	sampled.SamplePercent = 50

	if _, err := engine.Run(t.Context(), sampled); !errors.Is(err, engine.ErrBaselineWrite) {
		t.Fatalf("a sampled baseline write returned %v, want ErrBaselineWrite", err)
	}
}

// TestASampledFailOnNewIsRefusedWithoutTheOptIn extends the sampled row to
// --fail-on-new, and proves the opt-in's use is reported.
func TestASampledFailOnNewIsRefusedWithoutTheOptIn(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")

	config := baseConfig(t, module)
	config.FailOnNew = true
	config.HasSample = true
	config.SamplePercent = 50

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, engine.ErrSampledGate) {
		t.Fatalf("a sampled --fail-on-new returned %v, want ErrSampledGate", err)
	}

	config.AllowSampledGate = true

	result := runWith(t, config)

	if result.Sampling == nil || result.Sampling.Authoritative {
		t.Fatal("the sampled run is not labelled non-authoritative")
	}

	if result.Gates == nil || !result.Gates.FailOnNew.Evaluated {
		t.Fatal("the opted-in sampled gate was not evaluated and reported")
	}

	if !result.Sampling.GateOptIn {
		t.Fatal("the report does not record that --allow-sampled-gate was used")
	}
}

// TestScopedFailOnNewJudgesTheSelectedPopulationOnly: an unselected finding
// cannot fail a scoped gate, and the gate outcome is labelled partial.
func TestScopedFailOnNewJudgesTheSelectedPopulationOnly(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "all-killed")
	weakenTests(t, module)
	git(t, module, "add", "--all")
	commit(t, module, "weaken")

	// No baseline file: every finding is new. But the scoped run selects
	// nothing — the working tree is clean — so the gate has nothing to judge.
	scoped := baseConfig(t, module)
	scoped.FailOnNew = true
	scoped.Since = sinceHead

	result := runWith(t, scoped)

	if code := result.ExitCode(failOnNewGate()); code != report.ExitClean {
		t.Fatalf("unselected findings failed a scoped gate: exit %d", code)
	}

	if result.Gates == nil || !result.Gates.FailOnNew.Partial {
		t.Fatal("a scoped fail-on-new outcome is not labelled partial")
	}
}

// TestBaselineNeverSuppressesSafetyGates: an accepted baseline changes
// nothing about the refusal to run unsandboxed effects.
func TestBaselineNeverSuppressesSafetyGates(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")

	config := baseConfig(t, module)
	config.FailOnNew = true

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("the safety gate under a baseline returned %v, want ErrUnsandboxedEffects", err)
	}
}

// TestExitCodesAreDeterministicAcrossTheTable: the same configuration twice
// produces the same exit code, for the full and the accepted rows.
func TestExitCodesAreDeterministicAcrossTheTable(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	weakenTests(t, module)

	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true
	runWith(t, adopt)

	check := baseConfig(t, module)
	check.FailOnNew = true

	first := runWith(t, check).ExitCode(failOnNewGate())
	second := runWith(t, check).ExitCode(failOnNewGate())

	if first != second {
		t.Fatalf("the same gate configuration produced exits %d then %d", first, second)
	}
}

// TestTheCachedRowOfTheGateTable pins the truth table's cached run shape: a
// population served even partly from the cache reports no staleness, treats
// unmatched entries as unobserved, labels its gates partial, and refuses a
// baseline write with --no-cache as the remedy.
func TestTheCachedRowOfTheGateTable(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "all-killed")
	weakenTests(t, module)

	adopt := baseConfig(t, module)
	adopt.WriteBaseline = true
	adopted := runWith(t, adopt)

	if adopted.Gates.Baseline.Write != report.BaselineWritten {
		t.Fatal("the fresh full run could not write the baseline")
	}

	// The same configuration replays from the cache.
	cached := adopt
	cached.WriteBaseline = false
	cached.FailOnNew = true

	replayed := runWith(t, cached)
	if replayed.Population.Cached == 0 {
		t.Fatal("the second run replayed nothing; the cached row is not being exercised")
	}

	baseline := replayed.Gates.Baseline
	if baseline.StalenessReported || len(baseline.Stale) != 0 {
		t.Fatalf("a cached population reported staleness: %+v", baseline)
	}

	if !replayed.Gates.FailOnNew.Partial || replayed.Gates.FailOnNew.Scope != "selected" {
		t.Fatalf("a cached population's gate is not labelled partial over selected: %+v",
			replayed.Gates.FailOnNew)
	}

	// A write over a cached population is refused, loudly.
	rewrite := adopt
	if _, err := engine.Run(t.Context(), rewrite); !errors.Is(err, engine.ErrBaselineWrite) {
		t.Fatalf("a cached baseline write returned %v, want ErrBaselineWrite", err)
	}
}

// TestScopedMinScoreIsLabelledPartial completes the table's --since row for
// --min-score: evaluated over the selected population, labelled partial.
func TestScopedMinScoreIsLabelledPartial(t *testing.T) {
	t.Parallel()

	module := gitFixture(t, "all-killed")

	config := baseConfig(t, module)
	config.Since = sinceHead
	config.HasMinScore = true
	config.MinScore = 0
	config.NoCache = true

	result := runWith(t, config)

	if !result.Gates.MinScore.Evaluated || !result.Gates.MinScore.Partial ||
		result.Gates.MinScore.Scope != "selected" {
		t.Fatalf("a scoped --min-score outcome is not labelled partial over selected: %+v",
			result.Gates.MinScore)
	}
}
