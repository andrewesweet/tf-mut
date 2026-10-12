package engine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// fullScope is the gate scope a fully-observed population is evaluated over;
// the mask normalises the scope fields to it.
const fullScope = "full"

// assertVerdictInvariance is the verdict-invariance harness (#45), reused by
// every count lever: for any mutant present in both runs, state, diagnosis and
// evidence must be identical. Provenance is exempt by construction — it exists
// to record how the mutant was selected and executed, which is exactly what a
// lever changes.
func assertVerdictInvariance(t *testing.T, full, scoped report.Report) {
	t.Helper()

	byID := map[string]report.Mutant{}
	for _, mutant := range full.Mutants {
		byID[mutant.ID] = mutant
	}

	shared := 0

	for _, mutant := range scoped.Mutants {
		reference, found := byID[mutant.ID]
		if !found {
			continue
		}

		shared++

		if mutant.State != reference.State {
			t.Errorf("mutant %s (%s): state %s under the lever, %s in the full run",
				mutant.ID, mutant.Site, mutant.State, reference.State)

			continue
		}

		if canonicalVerdict(t, mutant.Verdict) != canonicalVerdict(t, reference.Verdict) {
			t.Errorf("mutant %s (%s): verdict differs between the lever and the full run",
				mutant.ID, mutant.Site)
		}
	}

	if shared == 0 {
		t.Fatal("the two runs share no mutants; the invariance claim is vacuous")
	}
}

// assertCachedAndFreshReportsByteIdentical is the control for the cache's
// silent-wrong-verdict class: everything the report publishes about a verdict
// must be byte-identical between a fresh run and a cached replay of the same
// tree — states, findings, evidence, diagnostics, suppressions, the run
// observations and the metrics. The fields masked here record HOW each verdict
// was obtained, which is exactly what a replay is allowed to change: the
// provenance's execution flag and cache key, the population's cached/fresh
// split, and the wall-clock telemetry with the baseline duration it calibrates.
func assertCachedAndFreshReportsByteIdentical(t *testing.T, fresh, cached report.Report) {
	t.Helper()

	how := func(r report.Report) report.Report {
		r.Population.Cached = 0
		r.Population.Fresh = r.Population.Selected
		r.Baseline.DurationMS = 0
		r.Telemetry = nil

		// A cached run honestly records the min-score gate as evaluated over a
		// partially-observed population — the scope rule counts every cached
		// verdict — so the scope and partiality are how-the-run facts too. The
		// gate's verdict itself is not masked: it is decided from the metrics,
		// which are compared.
		r.Gates.MinScore.Scope = fullScope
		r.Gates.MinScore.Partial = false
		r.Gates.FailOnNew.Scope = fullScope
		r.Gates.FailOnNew.Partial = false

		for index := range r.Mutants {
			if r.Mutants[index].Provenance != nil {
				r.Mutants[index].Provenance.Execution = report.ExecutionFresh
				r.Mutants[index].Provenance.CacheKey = ""
			}

			r.Mutants[index].Telemetry = nil
		}

		return r
	}

	freshDocument := canonicalReport(t, how(fresh))
	cachedDocument := canonicalReport(t, how(cached))

	if freshDocument == cachedDocument {
		return
	}

	freshLines := strings.Split(freshDocument, "\n")
	cachedLines := strings.Split(cachedDocument, "\n")

	for line := range min(len(freshLines), len(cachedLines)) {
		if freshLines[line] != cachedLines[line] {
			t.Fatalf("the cached replay's report differs from the fresh run's at line %d:\n fresh: %s\ncached: %s",
				line+1, freshLines[line], cachedLines[line])
		}
	}

	t.Fatalf("the cached replay's report differs in length: %d lines versus %d",
		len(freshLines), len(cachedLines))
}

// canonicalReport renders a report for byte comparison, indented so a
// failure can name the first line that differs.
func canonicalReport(t *testing.T, value report.Report) string {
	t.Helper()

	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encoding report: %v", err)
	}

	return string(encoded)
}

// canonicalVerdict renders a verdict for comparison: diagnosis and evidence,
// byte for byte.
func canonicalVerdict(t *testing.T, verdict *report.Verdict) string {
	t.Helper()

	if verdict == nil {
		return ""
	}

	encoded, err := json.Marshal(verdict)
	if err != nil {
		t.Fatalf("encoding verdict: %v", err)
	}

	return string(encoded)
}
