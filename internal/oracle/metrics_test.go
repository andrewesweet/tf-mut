package oracle_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
)

// TestAnErroredRowMarksAScoredPopulationIncomplete is the D18 second half:
// a mutant that could not be evaluated at all leaves its verdict unobserved,
// so the score is incomplete even when every other mutant scored. Without
// the error the same population reads complete.
func TestAnErroredRowMarksAScoredPopulationIncomplete(t *testing.T) {
	t.Parallel()

	scored := []oracle.Classified{
		{Operator: mutation.BoolLiteralFlip, Outcome: oracle.NoCoverage("")},
		{Operator: mutation.BoolLiteralFlip, Outcome: oracle.NoCoverage("")},
	}

	complete := oracle.ComputeMetrics(oracle.NewPopulation(scored, oracle.Provenance{}))
	if complete.Scored != 2 {
		t.Fatalf("scored = %d, want 2: %+v", complete.Scored, complete)
	}

	if complete.Incomplete {
		t.Fatalf("a fully scored population with no error must read complete: %+v", complete)
	}

	errored := oracle.ComputeMetrics(oracle.NewPopulation(scored, oracle.Provenance{Errored: 1}))
	if errored.Scored != 2 {
		t.Fatalf("scored = %d, want 2: %+v", errored.Scored, errored)
	}

	if !errored.Incomplete {
		t.Fatal("a scored population with one errored row must read incomplete")
	}
}
