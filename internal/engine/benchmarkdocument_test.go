package engine_test

import (
	"os"
	"strings"
	"testing"
)

// The M5d document audit (#164): the published research document and the
// product-design roadmap carry the comparability posture the ticket decided,
// and the csv reporter row — aspirational, with no consumer in internal/report
// — stays retired.
func TestTheBenchmarkDocumentAndRoadmapStateTheComparabilityLimits(t *testing.T) {
	t.Parallel()

	document, err := os.ReadFile("../../docs/research/21-m5-benchmark.md")
	if err != nil {
		t.Fatalf("reading the M5d document: %v", err)
	}

	doc := string(document)

	// The phrase the review retired appears nowhere in the document: the
	// benchmark is side by side with Oasis on named axes, with stated
	// limitations — never "directly comparable".
	if strings.Contains(doc, "directly comparable") {
		t.Error(`the document claims the benchmark is "directly comparable" with Oasis`)
	}

	// Both tables the ticket names are present, in modules and in Oasis's
	// units.
	for _, table := range []string{"## The module-admission table", "## The mutant-level table"} {
		if !strings.Contains(doc, table) {
			t.Errorf("the document carries no %q section", table)
		}
	}

	// The equations are published beside the numbers they produced.
	for _, equation := range []string{"mutation score =", "assertion score =", "reachability ="} {
		if !strings.Contains(doc, equation) {
			t.Errorf("the document does not state the %q equation", strings.TrimSuffix(equation, " ="))
		}
	}

	// The limitations are stated before the Oasis side-by-side, so a reader
	// meets them first.
	limitations := strings.Index(doc, "## The limitations")
	sideBySide := strings.Index(doc, "## Side by side with Oasis")
	if limitations < 0 || sideBySide < 0 {
		t.Fatal("the document lacks the limitations or the Oasis side-by-side section")
	}

	if limitations > sideBySide {
		t.Error("the limitations are stated after the Oasis side-by-side")
	}

	// The portable-assertion story is on the page: what the two legs prove
	// about a module, and what the wall clock is published as and only as.
	if !strings.Contains(doc, "portable assertion") {
		t.Error("the document does not name the portable assertions")
	}

	design, err := os.ReadFile("../../docs/design/product-design.md")
	if err != nil {
		t.Fatalf("reading the product design: %v", err)
	}

	product := string(design)

	// The retired csv reporter row stays retired: no consumer exists in
	// internal/report.
	if strings.Contains(product, "| csv |") {
		t.Error(`product-design still carries a "| csv |" reporter row`)
	}

	// The roadmap's narrowed claim stands: side-by-side evaluation on named
	// axes with stated limitations, never the inflated phrase.
	if strings.Contains(product, "directly comparable") {
		t.Error(`product-design claims the benchmark is "directly comparable" with Oasis`)
	}

	if !strings.Contains(product, "side-by-side evaluation") {
		t.Error("product-design no longer states the side-by-side evaluation claim")
	}
}
