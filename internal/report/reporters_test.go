package report_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// Literals the findings-rendering tests share.
const (
	findingMessage   = "the mutant survived"
	findingFix       = "add an assertion"
	statusNew        = "new"
	statusAccepted   = "accepted"
	unassertableSite = "output.value"
)

// mteSchemaPath is the pinned, vendored mutation-testing-report-schema.
const mteSchemaPath = "../../docs/schema/mutation-testing-report-2.0.0.json"

// junitSchemaPath is the vendored Jenkins JUnit dialect.
const junitSchemaPath = "../../docs/schema/junit-jenkins.xsd"

// allStatesMetrics is the sample population's arithmetic: one mutant per
// state, two survivors, so seven scored and one timeout making the score
// incomplete.
func allStatesMetrics() report.Metrics {
	return report.Metrics{
		MutationScore:  0.2857142857142857,
		AssertionScore: 0.2,
		Reachability:   0.7142857142857143,
		Incomplete:     true,
		Counts: map[report.State]int{
			report.Killed: 1, report.KilledByError: 1, report.Survived: 2,
			report.StructurallyUnassertable: 1, report.Unobservable: 1,
			report.NoCoverage: 1, report.Ignored: 1, report.Invalid: 1, report.Timeout: 1,
		},
		Diagnoses: map[report.Diagnosis]int{
			report.WeakAssertion: 1, report.IndeterminateUnknownValues: 1,
		},
		Scored: 7,
	}
}

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
			Operator: sampleNullOperator,
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
	value.Metrics = allStatesMetrics()
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

// The validator's refusals, one static error per rule (err113).
var (
	errUndeclaredElement   = errors.New("element is not declared")
	errUnclosedElement     = errors.New("document ended inside an open element")
	errUndeclaredAttribute = errors.New("attribute is not declared")
	errAttributePattern    = errors.New("attribute value violates its type restriction")
	errMissingAttribute    = errors.New("required attribute is missing")
	errChildNotPermitted   = errors.New("element is not permitted here")
	errOccursTooOften      = errors.New("element occurs above its bound")
	errOccursTooSeldom     = errors.New("element occurs below its bound")
	errOutOfSequence       = errors.New("element is out of sequence")
)

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
				return fmt.Errorf("%w: %q", errUndeclaredElement, name)
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
		return fmt.Errorf("%w: %q", errUnclosedElement, stack[len(stack)-1].name)
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
			return fmt.Errorf("%w: %q", errUndeclaredAttribute, attribute.Name.Local)
		}

		if pattern != nil && !pattern.MatchString(attribute.Value) {
			return fmt.Errorf("%w: %q=%q", errAttributePattern,
				attribute.Name.Local, attribute.Value)
		}

		seen[attribute.Name.Local] = true
	}

	for required := range declared.required {
		if !seen[required] {
			return fmt.Errorf("%w: %q", errMissingAttribute, required)
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
				return fmt.Errorf("%w: %q inside %q", errChildNotPermitted, child, name)
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
				return fmt.Errorf("%w: %q occurs more than %d time(s) inside %q",
					errOccursTooOften, particle.name, particle.max, name)
			}
		}

		if count < particle.min {
			return fmt.Errorf("%w: %q occurs %d time(s) inside %q; the sequence requires %d",
				errOccursTooSeldom, particle.name, count, name, particle.min)
		}
	}

	if index != len(children) {
		return fmt.Errorf("%w: %q inside %q", errOutOfSequence, children[index], name)
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

type xsPattern struct {
	Value string `xml:"value,attr"`
}

type xsSimpleType struct {
	Name    string    `xml:"name,attr"`
	Pattern xsPattern `xml:"restriction>pattern"`
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

// sitesReport builds findings across several sites and files, deliberately in
// generation order rather than location order, so grouping, per-finding
// file:line rendering and the group ordering are all observable. Two mutants
// share one site with different lines; the others sit in their own files.
func sitesReport() report.Report {
	lines := []struct {
		id       string
		operator string
		state    report.State
		site     string
		file     string
		line     int
		status   string
	}{
		{"aaaa00000001", "NUM-OFF-BY-ONE", report.Survived, "local.locations", sampleFile, 21, statusNew},
		{"aaaa00000002", "BOOL-FLIP", report.Survived, "local.locations", sampleFile, 23, statusAccepted},
		{"aaaa00000003", "STR-CASE", report.Survived, "var.type", "variables.tf", 9, statusAccepted},
		{"aaaa00000004", "EXT-BODY-BLANK", report.StructurallyUnassertable, unassertableSite, "outputs.tf", 5, ""},
	}

	mutants := make([]report.Mutant, 0, len(lines))

	for _, entry := range lines {
		mutant := report.Mutant{ //nolint:exhaustruct // the rendered subset.
			ID:       entry.id,
			Operator: entry.operator,
			Tier:     smokeTier,
			Module:   ".",
			Site:     entry.site,
			Range: report.Range{
				File:  entry.file,
				Start: report.Position{Line: entry.line, Column: 1},
				End:   report.Position{Line: entry.line, Column: 12},
			},
			State: entry.state,
			Verdict: &report.Verdict{ //nolint:exhaustruct // message and fix suffice.
				Message: findingMessage,
				Fix:     findingFix,
			},
		}

		if entry.state == report.Survived {
			mutant.Verdict.Diagnosis = report.WeakAssertion
		}

		if entry.status != "" {
			mutant.Provenance = &report.Provenance{ //nolint:exhaustruct // the labelled subset.
				Selection:      report.SelectionFull,
				Execution:      report.ExecutionFresh,
				BaselineStatus: entry.status,
			}
		}

		mutants = append(mutants, mutant)
	}

	value := sampleReport()
	value.Mutants = mutants
	value.Metrics = report.Metrics{ //nolint:exhaustruct // the rendered subset.
		Counts:    map[report.State]int{report.Survived: 3, report.StructurallyUnassertable: 1},
		Diagnoses: map[report.Diagnosis]int{report.WeakAssertion: 3},
		Scored:    4,
	}

	return value
}

// renderMarkdown is the one rendering path the markdown assertions share.
func renderMarkdown(t *testing.T, value report.Report) string {
	t.Helper()

	rendered := strings.Builder{}
	if err := report.WriteMarkdown(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	return rendered.String()
}

// TestMarkdownFindingsCarryFileLineGroupedBySite: the PR summary is a to-do
// list a reader can act on without opening the JSON report — every finding
// names the file and line it sits at, and the findings are grouped under
// their mutation site in location order rather than generation order.
func TestMarkdownFindingsCarryFileLineGroupedBySite(t *testing.T) {
	t.Parallel()

	page := renderMarkdown(t, sitesReport())

	for _, heading := range []string{
		"#### `local.locations` — main.tf:21",
		"#### `output.value` — outputs.tf:5",
		"#### `var.type` — variables.tf:9",
	} {
		if !strings.Contains(page, heading) {
			t.Fatalf("the markdown summary is missing the site heading %q:\n%s", heading, page)
		}
	}

	for _, line := range []string{
		"- `NUM-OFF-BY-ONE` Survived at main.tf:21 — weak-assertion (new)",
		"- `BOOL-FLIP` Survived at main.tf:23 — weak-assertion",
		"- `STR-CASE` Survived at variables.tf:9 — weak-assertion",
		"- `EXT-BODY-BLANK` StructurallyUnassertable at outputs.tf:5",
	} {
		if !strings.Contains(page, line) {
			t.Fatalf("the markdown summary is missing the finding line %q:\n%s", line, page)
		}
	}

	main, outputs, variables := strings.Index(page, "local.locations"),
		strings.Index(page, "output.value"), strings.Index(page, "var.type")

	if main < 0 || outputs <= main || variables <= outputs {
		t.Fatalf("the site groups are not in location order (main < outputs < variables):\n%s", page)
	}
}

// TestMarkdownOmitsTheEmptyGateTable: a run that requested no gate and
// judged no baseline renders no gate table — a two-line table with no rows
// is noise where a decision should be. A populated table still renders.
func TestMarkdownOmitsTheEmptyGateTable(t *testing.T) {
	t.Parallel()

	value := allStatesReport()
	value.Gates = &report.Gates{ //nolint:exhaustruct // the unevaluated shape a plain run carries.
		MinScore:  report.GateOutcome{Evaluated: false},
		FailOnNew: report.GateOutcome{Evaluated: false},
	}

	if page := renderMarkdown(t, value); strings.Contains(page, "| Gate | Outcome |") {
		t.Fatalf("the markdown summary renders a gate table with no rows:\n%s", page)
	}

	if page := renderMarkdown(t, allStatesReport()); !strings.Contains(page, "| Gate | Outcome |") {
		t.Fatal("the markdown summary dropped the populated gate table")
	}
}

// TestMarkdownTruncationNamesEveryHiddenSite: the site cap bounds the
// summary without hiding anything — every site beyond it is named with its
// finding count, so the overflow lists what exists even where it does not
// render the findings themselves.
func TestMarkdownTruncationNamesEveryHiddenSite(t *testing.T) {
	t.Parallel()

	mutants := make([]report.Mutant, 0, 27)

	for index := range 27 {
		mutants = append(mutants, report.Mutant{ //nolint:exhaustruct // the rendered subset.
			ID:       fmt.Sprintf("aaaa%08d", index),
			Operator: "NUM-OFF-BY-ONE",
			Tier:     smokeTier,
			Module:   ".",
			Site:     fmt.Sprintf("resource_%02d.attr", index),
			Range: report.Range{
				File:  sampleFile,
				Start: report.Position{Line: index + 1, Column: 1},
				End:   report.Position{Line: index + 1, Column: 12},
			},
			State: report.Survived,
			Verdict: &report.Verdict{ //nolint:exhaustruct // message and fix suffice.
				Diagnosis: report.WeakAssertion,
				Message:   findingMessage,
				Fix:       findingFix,
			},
		})
	}

	value := sampleReport()
	value.Mutants = mutants
	value.Metrics = report.Metrics{ //nolint:exhaustruct // the rendered subset.
		Counts:    map[report.State]int{report.Survived: 27},
		Diagnoses: map[report.Diagnosis]int{report.WeakAssertion: 27},
		Scored:    27,
	}

	page := renderMarkdown(t, value)

	if !strings.Contains(page, "… and 2 more sites") {
		t.Fatalf("the overflow does not name the hidden-site count:\n%s", page)
	}

	for index := range 27 {
		if !strings.Contains(page, fmt.Sprintf("resource_%02d", index)) {
			t.Fatalf("site resource_%02d appears nowhere in the summary; the overflow hides sites", index)
		}
	}

	// The cap bounds the rendered findings, not the named sites: a hidden
	// site's finding lines are not rendered.
	if strings.Contains(page, "- `NUM-OFF-BY-ONE` Survived at main.tf:26") {
		t.Fatal("a hidden site's findings rendered despite the cap")
	}
}

// indeterminateReport is sitesReport with three survivors made
// indeterminate-unknown-values: two share one unknown-path set and one
// carries its own, so grouping by shared paths is observable alongside the
// site-grouped findings that remain.
func indeterminateReport() report.Report {
	value := sitesReport()

	shared := []string{
		"kubernetes_deployment_v1.workspace[0].id",
		"kubernetes_deployment_v1.workspace[0].metadata[0].uid",
	}

	for index := range value.Mutants {
		mutant := &value.Mutants[index]

		switch mutant.ID {
		case "aaaa00000001", "aaaa00000002":
			mutant.Verdict.Diagnosis = report.IndeterminateUnknownValues
			mutant.Verdict.Evidence = report.Evidence{UnknownPaths: shared} //nolint:exhaustruct // the grouped subset.
		case "aaaa00000003":
			mutant.Verdict.Diagnosis = report.IndeterminateUnknownValues
			mutant.Verdict.Evidence = report.Evidence{ //nolint:exhaustruct // the grouped subset.
				UnknownPaths: []string{"aws_iam_role.this[0].unique_id"},
			}
		default:
			// The remaining finding stays as sitesReport built it.
		}
	}

	return value
}

// TestMarkdownGroupsIndeterminateSurvivorsBySharedUnknownPaths: survivors
// indeterminate for the same reason read as one finding — the shared paths
// are stated once per group with the group's size, not once per survivor.
func TestMarkdownGroupsIndeterminateSurvivorsBySharedUnknownPaths(t *testing.T) {
	t.Parallel()

	page := renderMarkdown(t, indeterminateReport())

	for _, want := range []string{
		"**2 survivors** are indeterminate because these paths are unknown at plan:",
		"**1 survivor** is indeterminate because these paths are unknown at plan:",
		"- `kubernetes_deployment_v1.workspace[0].id`",
		"- `kubernetes_deployment_v1.workspace[0].metadata[0].uid`",
		"- `aws_iam_role.this[0].unique_id`",
		// The grouped survivors are still named, with their own file:line.
		"- `NUM-OFF-BY-ONE` Survived at main.tf:21 (new)",
		"- `BOOL-FLIP` Survived at main.tf:23",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the markdown summary is missing %q:\n%s", want, page)
		}
	}

	// Said once per path set, not once per survivor: two sentences for two
	// groups, whatever the membership.
	if said := strings.Count(page, "because these paths are unknown at plan"); said != 2 {
		t.Fatalf("the indeterminate sentence appears %d times, want once per group (2)", said)
	}

	// A site whose every survivor is indeterminate has no site group left.
	if strings.Contains(page, "#### `local.locations`") {
		t.Fatalf("an all-indeterminate site still rendered as a site group:\n%s", page)
	}

	if !strings.Contains(page, "#### `output.value`") {
		t.Fatalf("the surviving site group disappeared:\n%s", page)
	}
}

// TestTerminalGroupsIndeterminateSurvivorsBySharedUnknownPaths: the
// terminal to-do list says once per path set that N survivors are
// indeterminate because these paths are unknown at plan, names the paths,
// and lists the group's members — instead of one indeterminate verdict per
// survivor hiding the shared cause.
func TestTerminalGroupsIndeterminateSurvivorsBySharedUnknownPaths(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteTerminal(&rendered, indeterminateReport()); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	page := rendered.String()

	for _, want := range []string{
		"  2 survivors are indeterminate because these paths are unknown at plan:",
		"  1 survivor is indeterminate because these paths are unknown at plan:",
		"    kubernetes_deployment_v1.workspace[0].id",
		"    kubernetes_deployment_v1.workspace[0].metadata[0].uid",
		"    aws_iam_role.this[0].unique_id",
		"    NUM-OFF-BY-ONE  main.tf:21  local.locations",
		"    BOOL-FLIP  main.tf:23  local.locations",
		"    STR-CASE  variables.tf:9  var.type",
		"    Fix: add an assertion",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the terminal report is missing %q:\n%s", want, page)
		}
	}

	if said := strings.Count(page, "because these paths are unknown at plan"); said != 2 {
		t.Fatalf("the indeterminate sentence appears %d times, want once per group (2)", said)
	}
}

// TestWarningsReachEveryReporter: the report's warnings — preview's
// safety-gate outcome among them — travel through every reporter, each in
// its format's natural place. Terminal and JSON already rendered them; the
// other five discarded them.
func TestWarningsReachEveryReporter(t *testing.T) {
	t.Parallel()

	value := allStatesReport()
	value.Warnings = []string{
		"the safety gates would refuse a mutation run: unsandboxed effects at main.tf:9",
		"1 file(s) are not canonically formatted",
	}

	t.Run("markdown", func(t *testing.T) {
		t.Parallel()

		assertMarkdownCarriesWarnings(t, value)
	})

	t.Run("html", func(t *testing.T) {
		t.Parallel()

		assertHTMLCarriesWarnings(t, value)
	})

	t.Run("sarif", func(t *testing.T) {
		t.Parallel()

		assertSARIFCarriesWarnings(t, value)
	})

	t.Run("junit", func(t *testing.T) {
		t.Parallel()

		assertJUnitCarriesWarnings(t, value)
	})

	t.Run("mte", func(t *testing.T) {
		t.Parallel()

		assertMTECarriesWarnings(t, value)
	})
}

// assertMarkdownCarriesWarnings holds the summary to a warnings section
// carrying every warning verbatim.
func assertMarkdownCarriesWarnings(t *testing.T, value report.Report) {
	t.Helper()

	page := renderMarkdown(t, value)

	if !strings.Contains(page, "### Warnings") {
		t.Fatalf("the markdown summary has no warnings section:\n%s", page)
	}

	for _, warning := range value.Warnings {
		if !strings.Contains(page, warning) {
			t.Fatalf("the markdown summary dropped the warning %q:\n%s", warning, page)
		}
	}
}

// assertHTMLCarriesWarnings holds the page to human-visible warnings: in
// tf-mut's own header section, above the embedded viewer.
func assertHTMLCarriesWarnings(t *testing.T, value report.Report) {
	t.Helper()

	rendered := strings.Builder{}
	if err := report.WriteHTML(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	page := rendered.String()

	for _, warning := range value.Warnings {
		if !strings.Contains(page, html.EscapeString(warning)) {
			t.Fatalf("the HTML page dropped the warning %q", warning)
		}
	}

	header := strings.Index(page, "authoritative metrics")
	viewer := strings.Index(page, "<mutation-test-report-app")
	warnings := strings.Index(page, html.EscapeString(value.Warnings[0]))

	if warnings < 0 || header >= warnings || warnings >= viewer {
		t.Fatal("the warnings do not render in the header section above the viewer")
	}
}

// assertSARIFCarriesWarnings holds the document to one warning-level tool
// execution notification per report warning, after the run summary.
func assertSARIFCarriesWarnings(t *testing.T, value report.Report) {
	t.Helper()

	rendered := strings.Builder{}
	if err := report.WriteSARIF(&rendered, value, nil); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	runs, ok := document[sarifRunsKey].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("the SARIF document carries %v runs", document[sarifRunsKey])
	}

	run, ok := runs[0].(map[string]any)
	if !ok {
		t.Fatal("the SARIF run is not an object")
	}

	invocations, ok := run[sarifInvocationsKey].([]any)
	if !ok || len(invocations) != 1 {
		t.Fatalf("the SARIF run carries %v invocations", run[sarifInvocationsKey])
	}

	invocation, ok := invocations[0].(map[string]any)
	if !ok {
		t.Fatal("the SARIF invocation is not an object")
	}

	notifications, ok := invocation["toolExecutionNotifications"].([]any)
	if !ok {
		t.Fatal("the SARIF invocation carries no toolExecutionNotifications")
	}

	// The run summary notification plus one per warning.
	if len(notifications) != 1+len(value.Warnings) {
		t.Fatalf("%d tool execution notifications, want %d (summary + warnings)",
			len(notifications), 1+len(value.Warnings))
	}

	for _, warning := range value.Warnings {
		if !sarifNotifiesWarning(notifications, warning) {
			t.Fatalf("no warning-level notification carries %q", warning)
		}
	}
}

// sarifNotifiesWarning reports whether a warning-level notification carries
// the text.
func sarifNotifiesWarning(notifications []any, warning string) bool {
	for _, entry := range notifications {
		notification, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		message, ok := notification[sarifMessageKey].(map[string]any)
		if !ok {
			continue
		}

		text, ok := message[sarifTextKey].(string)
		if !ok {
			continue
		}

		if notification["level"] == "warning" && strings.Contains(text, warning) {
			return true
		}
	}

	return false
}

// assertJUnitCarriesWarnings holds the suite to a system-out carrying every
// warning, in the dialect's own place for run-level messages.
func assertJUnitCarriesWarnings(t *testing.T, value report.Report) {
	t.Helper()

	rendered := strings.Builder{}
	if err := report.WriteJUnit(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := rendered.String()
	assertJUnitValidatesAgainstTheVendoredSchema(t, document)

	type testsuite struct {
		SystemOut string `xml:"system-out"`
	}

	decoded := struct {
		Suites []testsuite `xml:"testsuite"`
	}{}
	if err := xml.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(decoded.Suites) != 1 {
		t.Fatalf("expected one testsuite, got %d", len(decoded.Suites))
	}

	for _, warning := range value.Warnings {
		if !strings.Contains(decoded.Suites[0].SystemOut, warning) {
			t.Fatalf("the JUnit system-out dropped the warning %q:\n%s",
				warning, decoded.Suites[0].SystemOut)
		}
	}
}

// assertMTECarriesWarnings holds the document to the full warning list in
// the config's tf-mut block, beside the authoritative metrics, and to the
// pinned schema.
func assertMTECarriesWarnings(t *testing.T, value report.Report) {
	t.Helper()

	rendered := strings.Builder{}
	if err := report.WriteMTE(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	schema := loadSchemaFile(t, mteSchemaPath)
	if problems := validate(schema, schema, document, "$"); len(problems) > 0 {
		t.Fatalf("the MTE document does not validate:\n  %s", strings.Join(problems, "\n  "))
	}

	config, ok := document["config"].(map[string]any)
	if !ok {
		t.Fatal("the MTE document carries no config object")
	}

	embedded, ok := config["tf-mut"].(map[string]any)
	if !ok {
		t.Fatal("the MTE config carries no tf-mut block")
	}

	carried, ok := embedded["warnings"].([]any)
	if !ok || len(carried) != len(value.Warnings) {
		t.Fatalf("the MTE config carries %v, want all %d warnings", embedded["warnings"], len(value.Warnings))
	}

	for index, warning := range value.Warnings {
		text, ok := carried[index].(string)
		if !ok {
			t.Fatalf("the MTE config carries %v at %d, want the string %q", carried[index], index, warning)
		}

		if text != warning {
			t.Fatalf("the MTE config carries %q at %d, want %q", text, index, warning)
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

// TestSARIFSpeaksOnlyTheSchemaVocabulary: GitHub's code-scanning upload
// validates the document against the SARIF 2.1.0 schema and rejects any
// undeclared property — an invocation-level `message` cost the Action its
// annotations before this pin existed. Every emitted object is held to the
// property set the schema declares for it.
func TestSARIFSpeaksOnlyTheSchemaVocabulary(t *testing.T) {
	t.Parallel()

	rendered := strings.Builder{}
	if err := report.WriteSARIF(&rendered, allStatesReport(), nil); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	document := map[string]any{}
	if err := json.Unmarshal([]byte(rendered.String()), &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	for _, violation := range sarifVocabularyViolations("$document", document) {
		t.Error(violation)
	}
}

// TestTheSARIFVocabularyWalkRejectsTheEscapedWitness keeps the walk honest
// with the exact shape that escaped to GitHub once.
func TestTheSARIFVocabularyWalkRejectsTheEscapedWitness(t *testing.T) {
	t.Parallel()

	witness := map[string]any{
		sarifRunsKey: []any{map[string]any{
			sarifInvocationsKey: []any{map[string]any{
				sarifMessageKey: map[string]any{sarifTextKey: "x"},
			}},
		}},
	}

	if len(sarifVocabularyViolations("$document", witness)) == 0 {
		t.Fatal("the walk accepted an invocation-level message")
	}
}

// The property names the vocabulary and its witness both spell out.
const (
	sarifRunsKey        = "runs"
	sarifInvocationsKey = "invocations"
	sarifMessageKey     = "message"
	sarifTextKey        = "text"
)

// sarifVocabulary maps each object the reporter emits — keyed by the property
// naming it — to the properties the SARIF 2.1.0 schema declares for that
// object. The schema's free-form property bags are exempted in the walk.
func sarifVocabulary() map[string][]string {
	return map[string][]string{
		"$document":                  {"$schema", "version", sarifRunsKey},
		sarifRunsKey:                 {"tool", "results", sarifInvocationsKey},
		"tool":                       {"driver"},
		"driver":                     {"name", "informationUri", "rules"},
		"rules":                      {"id", "name", "shortDescription", "fullDescription", "help", "properties"},
		"results":                    {"ruleId", "level", sarifMessageKey, "locations", "partialFingerprints"},
		"locations":                  {"physicalLocation"},
		"physicalLocation":           {"artifactLocation", "region"},
		"artifactLocation":           {"uri"},
		"region":                     {"startLine", "startColumn", "endLine", "endColumn"},
		sarifInvocationsKey:          {"executionSuccessful", "toolExecutionNotifications"},
		"toolExecutionNotifications": {"level", sarifMessageKey},
		sarifMessageKey:              {sarifTextKey},
		"shortDescription":           {sarifTextKey},
		"fullDescription":            {sarifTextKey},
		"help":                       {sarifTextKey},
	}
}

func sarifVocabularyViolations(field string, value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		if field == "properties" || field == "partialFingerprints" {
			return nil
		}

		allowed, pinned := sarifVocabulary()[field]
		if !pinned {
			return []string{fmt.Sprintf("object %q has no pinned vocabulary", field)}
		}

		violations := []string{}

		for key, child := range typed {
			if !slices.Contains(allowed, key) {
				violations = append(violations,
					fmt.Sprintf("property %q is not declared on the SARIF %q object", key, field))

				continue
			}

			violations = append(violations, sarifVocabularyViolations(key, child)...)
		}

		return violations
	case []any:
		violations := []string{}

		for _, entry := range typed {
			violations = append(violations, sarifVocabularyViolations(field, entry)...)
		}

		return violations
	default:
		return nil
	}
}
