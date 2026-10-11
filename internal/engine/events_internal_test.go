package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestBaselineLabellingPreservesPublishedEvent(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"new", "accepted"} {
		for _, hasProvenance := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/provenance=%t", label, hasProvenance), func(t *testing.T) {
				t.Parallel()

				row := report.Mutant{ID: "survivor", State: report.Survived}
				wantProvenance := report.Provenance{
					Selection: report.SelectionFull, Execution: report.ExecutionFresh,
				}
				if hasProvenance {
					row.Provenance = &report.Provenance{
						Selection: report.SelectionFull, Reason: "full population",
						Execution: report.ExecutionFresh,
					}
					wantProvenance = *row.Provenance
				}

				before, err := json.Marshal(row)
				if err != nil {
					t.Fatalf("encoding the original event row: %v", err)
				}

				population := evaluatedPopulation{
					rows: make([]report.Mutant, 1), classified: make([]oracle.Classified, 1),
				}
				events := dispatcher{}
				events.mutantEvaluated.subscribe(population.observe)

				var published MutantEvaluated

				events.mutantEvaluated.subscribe(func(event MutantEvaluated) error {
					published = event

					return nil
				})
				if err := events.mutantEvaluated.publish(MutantEvaluated{index: 0, row: row}); err != nil {
					t.Fatalf("publishing the evaluated mutant: %v", err)
				}

				projection := gradingProjection{evaluated: population, labels: map[int]string{0: label}}
				result := projection.project()

				after, err := json.Marshal(published.row)
				if err != nil {
					t.Fatalf("encoding the published event row: %v", err)
				}

				if !bytes.Equal(before, after) {
					t.Fatalf("baseline labelling changed the published event:\nbefore: %s\nafter: %s", before, after)
				}

				wantProvenance.BaselineStatus = label
				row.Provenance = &wantProvenance
				if !reflect.DeepEqual(result.Mutants[0], row) {
					t.Fatalf("the labelled report row changed: got %+v, want %+v", result.Mutants[0], row)
				}
			})
		}
	}
}
