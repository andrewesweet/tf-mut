package engine_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// TestDefaultWiringMatchesTheDirectEntryPoint is the composition-root
// equivalence proof: the matrix fixture previewed through engine.Run and
// through engine.New(engine.DefaultDependencies()) renders byte-identical
// JSON reports.
//
// One exclusion applies, on the same terms as the M5 invariance proofs: the
// top-level telemetry block carries the run's own identity and cost — a
// random run identifier and this run's wall-clock milliseconds — so it is
// removed from both documents before the comparison. Every other byte is
// compared, so a wiring difference that moved any verdict, finding, metric
// or warning still goes red.
func TestDefaultWiringMatchesTheDirectEntryPoint(t *testing.T) {
	t.Parallel()

	// One module directory for both legs: the report carries the module's
	// absolute path, so the legs must differ by the wiring alone. A preview
	// executes nothing and writes nothing into the source tree, so sharing
	// the copy is safe.
	module := copyFixture(t, operatorsFixture)
	request := previewRequest(t, module)

	direct, err := engine.Run(t.Context(), &request)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}

	wired, err := engine.New(engine.DefaultDependencies()).Run(t.Context(), &request)
	if err != nil {
		t.Fatalf("engine.New(engine.DefaultDependencies()).Run: %v", err)
	}

	directJSON := renderedJSON(t, direct)
	wiredJSON := renderedJSON(t, wired)

	// The equivalence is over a real population: an identical pair of empty
	// reports would prove the wiring equal and nothing else.
	if len(direct.Mutants) == 0 {
		t.Fatal("the matrix preview described no mutants, so the equivalence is vacuous")
	}

	if !bytes.Equal(directJSON, wiredJSON) {
		t.Fatalf("the default wiring moved the matrix report at %s",
			firstDifferenceJSON(directJSON, wiredJSON))
	}
}

// renderedJSON renders the report as the published JSON document, minus the
// run's own telemetry block, re-encoded under canonical key order so the
// comparison is byte-exact.
func renderedJSON(t *testing.T, value report.Report) []byte {
	t.Helper()

	var rendered bytes.Buffer
	if err := report.WriteJSON(&rendered, value); err != nil {
		t.Fatalf("rendering the JSON report: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(rendered.Bytes(), &document); err != nil {
		t.Fatalf("decoding the JSON report: %v", err)
	}

	delete(document, "telemetry")

	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("re-encoding the JSON report: %v", err)
	}

	return canonical
}

// firstDifferenceJSON names the first top-level key whose content differs,
// so a red proof points at the moved field rather than dumping two whole
// documents.
func firstDifferenceJSON(first, second []byte) string {
	var firstDocument, secondDocument map[string]any
	if err := json.Unmarshal(first, &firstDocument); err != nil {
		return "first document does not decode"
	}

	if err := json.Unmarshal(second, &secondDocument); err != nil {
		return "second document does not decode"
	}

	for key, firstValue := range firstDocument {
		secondValue, ok := secondDocument[key]
		if !ok {
			return "key " + key + " is missing from the second document"
		}

		firstEncoded, firstErr := json.Marshal(firstValue)
		secondEncoded, secondErr := json.Marshal(secondValue)
		if firstErr != nil || secondErr != nil {
			return "key " + key + " does not re-encode"
		}

		if !bytes.Equal(firstEncoded, secondEncoded) {
			return "key " + key + " differs"
		}
	}

	for key := range secondDocument {
		if _, ok := firstDocument[key]; !ok {
			return "key " + key + " is missing from the first document"
		}
	}

	return "documents differ with identical top-level keys"
}
