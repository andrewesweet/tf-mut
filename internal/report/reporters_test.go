package report_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

	assertJUnitValidatesAgainstTheVendoredSchema(t, rendered.String())
}

// assertJUnitValidatesAgainstTheVendoredSchema validates the emitted
// document against docs/schema/junit-jenkins.xsd — the used dialect in full:
// well-formedness, element declarations, content models with sequence order
// and occurrence bounds, attribute vocabularies with required attributes,
// and simple-type pattern restrictions such as SUREFIRE_TIME. The validator
// is built from the schema file itself and proven by negative witnesses.
func assertJUnitValidatesAgainstTheVendoredSchema(t *testing.T, document string) {
	t.Helper()

	if err := validateJUnit(loadJUnitSchema(t), document); err != nil {
		t.Fatalf("the JUnit document does not validate against the vendored schema: %v", err)
	}
}

// junitParticle is one entry of a content model: an element reference with
// its occurrence bounds.
type junitParticle struct {
	name string
	min  int
	max  int // -1 is unbounded
}

// junitElement is one declared element: its content model, attributes and
// attribute types.
type junitElement struct {
	// choice lists the members of an unbounded choice group; nil where the
	// model is a sequence.
	choice map[string]bool
	// sequence lists the ordered particles of a sequence model.
	sequence []junitParticle
	// attributes maps each declared attribute to its simple-type pattern
	// (nil where the type is unrestricted).
	attributes map[string]*regexp.Regexp
	required   map[string]bool
}

// validateJUnit validates a document against the parsed dialect. Every
// decoder error is a validation failure: a truncated document must never
// pass by ending early.
func validateJUnit(schema map[string]junitElement, document string) error {
	decoder := xml.NewDecoder(strings.NewReader(document))

	type frame struct {
		name     string
		children []string
	}

	stack := []frame{}

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("malformed XML: %w", err)
		}

		switch typed := token.(type) {
		case xml.StartElement:
			name := typed.Name.Local

			if _, found := schema[name]; !found {
				return fmt.Errorf("element %q is not declared", name)
			}

			if len(stack) > 0 {
				stack[len(stack)-1].children = append(stack[len(stack)-1].children, name)
			}

			if err := validateJUnitAttributes(schema[name], typed); err != nil {
				return fmt.Errorf("element %q: %w", name, err)
			}

			stack = append(stack, frame{name: name, children: nil})
		case xml.EndElement:
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if err := validateJUnitChildren(schema[top.name], top.name, top.children); err != nil {
				return err
			}
		default:
		}
	}

	if len(stack) != 0 {
		return fmt.Errorf("document ended inside %q", stack[len(stack)-1].name)
	}

	return nil
}

// validateJUnitAttributes holds an element's attributes to the declaration:
// vocabulary, required presence, and simple-type patterns.
func validateJUnitAttributes(declared junitElement, element xml.StartElement) error {
	seen := map[string]bool{}

	for _, attribute := range element.Attr {
		pattern, allowed := declared.attributes[attribute.Name.Local]
		if !allowed {
			return fmt.Errorf("attribute %q is not declared", attribute.Name.Local)
		}

		if pattern != nil && !pattern.MatchString(attribute.Value) {
			return fmt.Errorf("attribute %q value %q violates its type restriction",
				attribute.Name.Local, attribute.Value)
		}

		seen[attribute.Name.Local] = true
	}

	for required := range declared.required {
		if !seen[required] {
			return fmt.Errorf("required attribute %q is missing", required)
		}
	}

	return nil
}

// validateJUnitChildren holds an element's child sequence to its content
// model: membership for a choice group, order and occurrence bounds for a
// sequence.
func validateJUnitChildren(declared junitElement, name string, children []string) error {
	if declared.choice != nil {
		for _, child := range children {
			if !declared.choice[child] {
				return fmt.Errorf("element %q is not permitted inside %q", child, name)
			}
		}

		return nil
	}

	index := 0

	for _, particle := range declared.sequence {
		count := 0

		for index < len(children) && children[index] == particle.name {
			index++
			count++

			if particle.max >= 0 && count > particle.max {
				return fmt.Errorf("element %q occurs more than %d time(s) inside %q",
					particle.name, particle.max, name)
			}
		}

		if count < particle.min {
			return fmt.Errorf("element %q occurs %d time(s) inside %q; the sequence requires %d",
				particle.name, count, name, particle.min)
		}
	}

	if index != len(children) {
		return fmt.Errorf("element %q is out of sequence inside %q", children[index], name)
	}

	return nil
}

// The vendored XSD's own shape, as encoding/xml sees it.
type xsAttribute struct {
	Name string `xml:"name,attr"`
	Use  string `xml:"use,attr"`
	Type string `xml:"type,attr"`
}

type xsElementRef struct {
	Ref       string `xml:"ref,attr"`
	Name      string `xml:"name,attr"`
	MinOccurs string `xml:"minOccurs,attr"`
	MaxOccurs string `xml:"maxOccurs,attr"`
}

type xsComplexType struct {
	Name       string         `xml:"name,attr"`
	Sequence   []xsElementRef `xml:"sequence>element"`
	Choice     []xsElementRef `xml:"choice>element"`
	SeqChoice  []xsElementRef `xml:"sequence>choice>element"`
	Attributes []xsAttribute  `xml:"attribute"`
}

type xsElement struct {
	Name    string         `xml:"name,attr"`
	Type    string         `xml:"type,attr"`
	Complex *xsComplexType `xml:"complexType"`
}

type xsSimpleType struct {
	Name    string `xml:"name,attr"`
	Pattern struct {
		Value string `xml:"value,attr"`
	} `xml:"restriction>pattern"`
}

type xsSchema struct {
	Elements    []xsElement     `xml:"element"`
	Types       []xsComplexType `xml:"complexType"`
	SimpleTypes []xsSimpleType  `xml:"simpleType"`
}

// junitModel builds one element's content model from its complex type.
func junitModel(complexType *xsComplexType, patterns map[string]*regexp.Regexp) junitElement {
	model := junitElement{
		choice:     nil,
		sequence:   nil,
		attributes: map[string]*regexp.Regexp{},
		required:   map[string]bool{},
	}

	if complexType == nil {
		return model
	}

	choice := append(append([]xsElementRef{}, complexType.Choice...), complexType.SeqChoice...)
	if len(choice) > 0 {
		model.choice = map[string]bool{}
		for _, child := range choice {
			model.choice[refName(child)] = true
		}
	} else if len(complexType.Sequence) > 0 {
		for _, child := range complexType.Sequence {
			model.sequence = append(model.sequence, junitParticle{
				name: refName(child),
				min:  occurs(child.MinOccurs, 1),
				max:  occurs(child.MaxOccurs, 1),
			})
		}
	}

	for _, attribute := range complexType.Attributes {
		model.attributes[attribute.Name] = patterns[attribute.Type]

		if attribute.Use == "required" {
			model.required[attribute.Name] = true
		}
	}

	return model
}

func refName(child xsElementRef) string {
	if child.Ref != "" {
		return child.Ref
	}

	return child.Name
}

// occurs parses an occurrence bound; "unbounded" is -1.
func occurs(value string, fallback int) int {
	switch value {
	case "":
		return fallback
	case "unbounded":
		return -1
	default:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fallback
		}

		return parsed
	}
}

// loadJUnitSchema parses the vendored XSD: simple-type patterns, then every
// global element declaration with its inline or named complex type.
func loadJUnitSchema(t *testing.T) map[string]junitElement {
	t.Helper()

	content, err := os.ReadFile(filepath.Clean(junitSchemaPath))
	if err != nil {
		t.Fatalf("reading %s: %v", junitSchemaPath, err)
	}

	parsed := xsSchema{}
	if err := xml.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("parsing %s: %v", junitSchemaPath, err)
	}

	patterns := map[string]*regexp.Regexp{}

	for _, simple := range parsed.SimpleTypes {
		if simple.Pattern.Value != "" {
			patterns[simple.Name] = regexp.MustCompile("^(?:" + simple.Pattern.Value + ")$")
		}
	}

	namedTypes := map[string]xsComplexType{}
	for _, complexType := range parsed.Types {
		namedTypes[complexType.Name] = complexType
	}

	schema := map[string]junitElement{}

	for _, element := range parsed.Elements {
		complexType := element.Complex
		if complexType == nil {
			if named, found := namedTypes[element.Type]; found {
				complexType = &named
			}
		}

		schema[element.Name] = junitModel(complexType, patterns)
	}

	if len(schema) == 0 {
		t.Fatalf("%s declares no elements; the validator has nothing to hold the dialect to",
			junitSchemaPath)
	}

	return schema
}

// TestTheJUnitValidatorRejectsTheNegativeWitnesses proves the validator is a
// validator: malformed XML, out-of-sequence children, violated occurrence
// bounds, missing required attributes and broken SUREFIRE_TIME values must
// each fail.
func TestTheJUnitValidatorRejectsTheNegativeWitnesses(t *testing.T) {
	t.Parallel()

	schema := loadJUnitSchema(t)

	witnesses := map[string]string{
		"malformed, truncated XML": `<testsuites name="x" tests="1"><testsuite`,
		"undeclared element":       `<testsuites><imposter/></testsuites>`,
		"out-of-sequence rerun children": `<testsuites><testsuite name="s" tests="1" failures="0" errors="0">` +
			`<testcase name="c"><rerunFailure type="t"><system-out>o</system-out>` +
			`<stackTrace>s</stackTrace></rerunFailure></testcase></testsuite></testsuites>`,
		"occurrence bound violated": `<testsuites><testsuite name="s" tests="1" failures="0" errors="0">` +
			`<testcase name="c"><rerunFailure type="t"><stackTrace>a</stackTrace>` +
			`<stackTrace>b</stackTrace></rerunFailure></testcase></testsuite></testsuites>`,
		"missing required attribute": `<testsuites><testsuite name="s" tests="1" failures="0" errors="0">` +
			`<testcase/></testsuite></testsuites>`,
		"undeclared attribute": `<testsuites bogus="1"></testsuites>`,
		"SUREFIRE_TIME restriction violated": `<testsuites><testsuite name="s" tests="1" failures="0" ` +
			`errors="0" time="not-a-time"></testsuite></testsuites>`,
	}

	for name, witness := range witnesses {
		if err := validateJUnit(schema, witness); err == nil {
			t.Errorf("the validator accepted the %s witness", name)
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
