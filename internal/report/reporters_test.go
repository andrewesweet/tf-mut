package report_test

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3d.1 (#51): the travelling reporters, from the one 2.1.0 report value.
// The mutation-testing-elements document is a declared-lossy adapter whose
// score disagreement is tested rather than denied; the HTML page is
// self-contained; the JUnit dialect maps every state; the markdown carries
// the gate-table outcome and the new-versus-accepted split.

// smokeTier is the tier every synthetic mutant carries.
const smokeTier = "smoke"

// mteSchemaPath is the pinned, vendored mutation-testing-report-schema.
const mteSchemaPath = "../../docs/schema/mutation-testing-report-2.0.0.json"

// junitSchemaPath is the vendored Jenkins JUnit dialect.
const junitSchemaPath = "../../docs/schema/junit-jenkins.xsd"

// allStatesReport covers every state class once, so a mapping that loses one
// fails here.
func allStatesReport() report.Report {
	states := []struct {
		state     report.State
		diagnosis report.Diagnosis
	}{
		{report.Killed, ""},
		{report.KilledByError, ""},
		{report.Survived, report.WeakAssertion},
		{report.Survived, report.IndeterminateUnknownValues},
		{report.StructurallyUnassertable, ""},
		{report.Unobservable, ""},
		{report.NoCoverage, ""},
		{report.Ignored, ""},
		{report.Invalid, ""},
		{report.Timeout, ""},
	}

	mutants := make([]report.Mutant, 0, len(states))

	for index, entry := range states {
		mutant := report.Mutant{ //nolint:exhaustruct // the mapped subset.
			ID:       string(rune('a'+index)) + "00000000000",
			Operator: "EXT-OUTPUT-NULL",
			Tier:     smokeTier,
			Module:   ".",
			Site:     "output.value",
			Range: report.Range{
				File:  "main.tf",
				Start: report.Position{Line: 1, Column: 1},
				End:   report.Position{Line: 1, Column: 10},
			},
			State: entry.state,
			Runs:  []report.RunOutcome{},
		}

		if entry.state == report.Survived || entry.state == report.StructurallyUnassertable {
			mutant.Verdict = &report.Verdict{ //nolint:exhaustruct // message and fix suffice.
				Diagnosis: entry.diagnosis,
				Message:   "the mutant survived",
				Fix:       "add an assertion",
			}
		}

		if entry.state == report.Survived {
			status := "new"
			if entry.diagnosis == report.IndeterminateUnknownValues {
				status = "accepted"
			}

			mutant.Provenance = &report.Provenance{ //nolint:exhaustruct // the labelled subset.
				Selection:      report.SelectionFull,
				Execution:      report.ExecutionFresh,
				BaselineStatus: status,
			}
		}

		mutants = append(mutants, mutant)
	}

	value := sampleReport()
	value.Mutants = mutants
	value.Metrics = report.ComputeMetrics(mutants)
	value.Errors = []report.ExecutionError{{
		MutantID: "deadbeef0000", Site: "output.value",
		Message: "the fingerprint run could not be evaluated",
	}}
	value.Gates = &report.Gates{
		MinScore: report.GateOutcome{
			Evaluated: true, Scope: "full", Partial: false, Passed: false, Refused: "",
		},
		FailOnNew: report.GateOutcome{
			Evaluated: true, Scope: "full", Partial: false, Passed: false, Refused: "",
		},
		Baseline: &report.BaselineGate{
			Path: ".tf-mut-baseline.json", Accepted: 1, Matched: 1,
			New: []string{mutants[2].ID}, Stale: nil, Unobserved: nil,
			StalenessReported: true, Write: "",
		},
	}

	return value
}

// TestMTEDocumentValidatesAgainstThePinnedSchema: the adapter's output is
// schema-valid, and the required members are present.
func TestMTEDocumentValidatesAgainstThePinnedSchema(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteMTE(&rendered, allStatesReport()); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := any(nil)
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	schema := loadSchemaFile(t, mteSchemaPath)
	if problems := validate(schema, schema, document, "$"); len(problems) > 0 {
		t.Fatalf("the MTE document does not validate:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestTheScoreDisagreementIsAssertedNotDenied computes both scores and
// requires them to differ on a population where the mapping loses
// information: KilledByError leaves the numerator, StructurallyUnassertable
// leaves the denominator, Timeout joins the detected set.
func TestTheScoreDisagreementIsAssertedNotDenied(t *testing.T) {
	t.Parallel()

	value := allStatesReport()

	authoritative := value.Metrics.MutationScore
	viewer := report.MTEComputedScore(value)

	if authoritative == viewer {
		t.Fatalf("the authoritative score (%.3f) equals the viewer's (%.3f) on a population "+
			"built to expose the loss; the disagreement test is vacuous", authoritative, viewer)
	}
}

// TestAuthoritativeMetricsAreEmbeddedInTheDocument: consumers can read
// tf-mut's numbers from the config member without recomputing anything.
func TestAuthoritativeMetricsAreEmbeddedInTheDocument(t *testing.T) {
	t.Parallel()

	value := allStatesReport()

	rendered := strings.Builder{}
	if err := report.WriteMTE(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	config, hasConfig := document["config"].(map[string]any)
	if !hasConfig {
		t.Fatal("the document carries no config member")
	}

	embedded, found := config["tf-mut"].(map[string]any)
	if !found {
		t.Fatal("the document carries no tf-mut metadata block")
	}

	if embedded["mutation_score"] != value.Metrics.MutationScore {
		t.Fatalf("embedded mutation score %v differs from the authoritative %v",
			embedded["mutation_score"], value.Metrics.MutationScore)
	}
}

// TestHTMLIsSelfContainedWithPinnedViewerAndLicence: no network fetch, the
// pinned viewer version stated, the licence shipped, and the authoritative
// metrics rendered above the viewer element.
func TestHTMLIsSelfContainedWithPinnedViewerAndLicence(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteHTML(&rendered, allStatesReport()); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	page := rendered.String()

	// Self-contained: no external resource loads. The embedded viewer keeps a
	// documentation hyperlink — navigation, rendered offline — so the check
	// targets the loading constructs: script and img src, link href, CSS
	// imports and url() fetches.
	for _, external := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]+src\s*=\s*"https?://`),
		regexp.MustCompile(`(?i)<link[^>]+href\s*=\s*"https?://`),
		regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*"https?://`),
		regexp.MustCompile(`(?i)@import\s+url\(\s*['\"]?https?://`),
	} {
		if external.MatchString(page) {
			t.Fatalf("the HTML page loads an external resource (%s); it must be self-contained",
				external)
		}
	}

	if !strings.Contains(page, "mutation-testing-elements v"+report.MTEVersion) {
		t.Fatal("the page does not state the pinned mutation-testing-elements version")
	}

	if !strings.Contains(page, "Apache License") {
		t.Fatal("the embedded viewer's licence does not ship with the page")
	}

	header := strings.Index(page, "authoritative metrics")
	viewer := strings.Index(page, "<mutation-test-report-app")

	if header < 0 || viewer < 0 || header > viewer {
		t.Fatal("the authoritative metrics do not render above the viewer")
	}
}

// TestJUnitMapsEveryStateInTheVendoredDialect: one assertion per state-class
// mapping, and the document parses as the dialect's testsuites shape with
// consistent counts.
func TestJUnitMapsEveryStateInTheVendoredDialect(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteJUnit(&rendered, allStatesReport()); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	type outcome struct {
		Message string `xml:"message,attr"`
		Type    string `xml:"type,attr"`
	}

	type testcase struct {
		Name    string   `xml:"name,attr"`
		Failure *outcome `xml:"failure"`
		Error   *outcome `xml:"error"`
		Skipped *outcome `xml:"skipped"`
	}

	type testsuite struct {
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Errors   int        `xml:"errors,attr"`
		Skipped  int        `xml:"skipped,attr"`
		Cases    []testcase `xml:"testcase"`
	}

	type testsuites struct {
		XMLName xml.Name    `xml:"testsuites"`
		Suites  []testsuite `xml:"testsuite"`
	}

	decoded := testsuites{}
	if err := xml.Unmarshal([]byte(rendered.String()), &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(decoded.Suites) != 1 {
		t.Fatalf("expected one testsuite, got %d", len(decoded.Suites))
	}

	suite := decoded.Suites[0]

	// Two survivors and one unassertable fail; one timeout plus one
	// operational failure error; NoCoverage, Unobservable, Ignored and
	// Invalid skip; Killed and KilledByError pass silently.
	if suite.Failures != 3 || suite.Errors != 2 || suite.Skipped != 4 {
		t.Fatalf("counts failures=%d errors=%d skipped=%d, want 3/2/4",
			suite.Failures, suite.Errors, suite.Skipped)
	}

	if suite.Tests != len(suite.Cases) {
		t.Fatalf("tests attribute %d disagrees with %d cases", suite.Tests, len(suite.Cases))
	}

	failures := 0

	for _, entry := range suite.Cases {
		if entry.Failure != nil {
			failures++

			if !strings.Contains(entry.Failure.Message, "Survived") &&
				!strings.Contains(entry.Failure.Message, "StructurallyUnassertable") {
				t.Fatalf("a failure case does not carry its state: %q", entry.Failure.Message)
			}
		}
	}

	if failures != 3 {
		t.Fatalf("%d failure cases, want 3", failures)
	}

	assertJUnitShapeMatchesTheVendoredSchema(t, rendered.String())
}

// assertJUnitShapeMatchesTheVendoredSchema checks the emitted element and
// attribute names against the vendored XSD, so the dialect cannot drift
// silently. It is a structural check, not a full XSD evaluation, and it reads
// the schema it checks against.
func assertJUnitShapeMatchesTheVendoredSchema(t *testing.T, document string) {
	t.Helper()

	schema, err := os.ReadFile(filepath.Clean(junitSchemaPath))
	if err != nil {
		t.Fatalf("reading %s: %v", junitSchemaPath, err)
	}

	declaredElements := map[string]bool{}
	for _, match := range regexp.MustCompile(`xs:element name="([^"]+)"`).FindAllStringSubmatch(string(schema), -1) {
		declaredElements[match[1]] = true
	}

	declaredAttributes := map[string]bool{}
	for _, match := range regexp.MustCompile(`xs:attribute name="([^"]+)"`).FindAllStringSubmatch(string(schema), -1) {
		declaredAttributes[match[1]] = true
	}

	decoder := xml.NewDecoder(strings.NewReader(document))

	for {
		token, decodeErr := decoder.Token()
		if decodeErr != nil {
			break
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		if !declaredElements[start.Name.Local] {
			t.Fatalf("element %q is not declared by the vendored dialect schema", start.Name.Local)
		}

		for _, attribute := range start.Attr {
			if !declaredAttributes[attribute.Name.Local] {
				t.Fatalf("attribute %q on %q is not declared by the vendored dialect schema",
					attribute.Name.Local, start.Name.Local)
			}
		}
	}
}

// TestMarkdownCarriesGateOutcomeAndNewVersusAccepted: the PR summary shows
// the decision where it happens.
func TestMarkdownCarriesGateOutcomeAndNewVersusAccepted(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteMarkdown(&rendered, allStatesReport()); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	page := rendered.String()

	for _, needle := range []string{
		"`--fail-on-new`",
		"`--min-score`",
		"1 new, 2 accepted",
		"killed 1 + killed-by-error 1",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("the markdown summary is missing %q:\n%s", needle, page)
		}
	}
}
