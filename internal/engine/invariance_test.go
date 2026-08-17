package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

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
