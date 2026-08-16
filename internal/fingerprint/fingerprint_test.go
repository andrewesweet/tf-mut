package fingerprint_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The payload shapes below are the ones Terraform v1.15.8 emits, reduced to the
// members the oracle reads. The canonicaliser's contract is about shape, so a
// reduced shape is the honest unit here; the end-to-end cases live in the
// engine suite against the real binary.

const (
	testFile = "tests/unit.tftest.hcl"
	runName  = "defaults"
)

func planPayload(t *testing.T, body string) tfexec.RunPayload {
	t.Helper()

	return payloadOf(t, tfexec.PayloadPlan, `{"plan_format_version":"1.2",`+body+`}`)
}

func payloadOf(t *testing.T, kind, document string) tfexec.RunPayload {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()

	value := map[string]any{}
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decoding %s: %v", document, err)
	}

	return tfexec.RunPayload{File: testFile, Run: runName, Kind: kind, Value: value}
}

func canonical(t *testing.T, payloads ...tfexec.RunPayload) []fingerprint.Payload {
	t.Helper()

	result, err := fingerprint.Canonicalise(payloads)
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}

	return result
}

func TestAPayloadOutsideThePinnedVersionRangeIsAnOperationalFailure(t *testing.T) {
	t.Parallel()

	future := payloadOf(t, tfexec.PayloadPlan, `{"plan_format_version":"2.0","resource_changes":[]}`)

	_, err := fingerprint.Canonicalise([]tfexec.RunPayload{future})
	if !errors.Is(err, fingerprint.ErrFormatVersion) {
		t.Fatalf("error = %v, want %v", err, fingerprint.ErrFormatVersion)
	}

	if !strings.Contains(err.Error(), "2.0") {
		t.Fatalf("the refusal does not name the version it found: %v", err)
	}

	missing := payloadOf(t, tfexec.PayloadPlan, `{"resource_changes":[]}`)
	if _, err := fingerprint.Canonicalise([]tfexec.RunPayload{missing}); !errors.Is(err, fingerprint.ErrFormatVersion) {
		t.Fatalf("a payload with no format version: error = %v, want %v", err, fingerprint.ErrFormatVersion)
	}
}

func TestCanonicalisationIsDeterministicAndOrderIndependent(t *testing.T) {
	t.Parallel()

	first := planPayload(t, `"resource_changes":[
	  {"address":"terraform_data.a","change":{"after":{"input":"one"}}},
	  {"address":"terraform_data.b","change":{"after":{"input":"two"}}}]`)

	reordered := planPayload(t, `"resource_changes":[
	  {"address":"terraform_data.b","change":{"after":{"input":"two"}}},
	  {"address":"terraform_data.a","change":{"after":{"input":"one"}}}]`)

	got := fingerprint.Fingerprint(canonical(t, reordered))
	if want := fingerprint.Fingerprint(canonical(t, first)); got != want {
		t.Fatal("an addressed collection changed its fingerprint when Terraform reordered it")
	}

	changed := planPayload(t, `"resource_changes":[
	  {"address":"terraform_data.a","change":{"after":{"input":"ONE"}}},
	  {"address":"terraform_data.b","change":{"after":{"input":"two"}}}]`)

	if fingerprint.Fingerprint(canonical(t, changed)) == fingerprint.Fingerprint(canonical(t, first)) {
		t.Fatal("a changed attribute did not move the fingerprint")
	}
}

func TestNullAndAbsentAndEmptyAreDistinguished(t *testing.T) {
	t.Parallel()

	null := planPayload(t, `"resource_changes":[{"address":"a.b","change":{"after":{"input":null}}}]`)
	absent := planPayload(t, `"resource_changes":[{"address":"a.b","change":{"after":{}}}]`)
	empty := planPayload(t, `"resource_changes":[{"address":"a.b","change":{"after":{"input":{}}}}]`)

	prints := []string{
		fingerprint.Fingerprint(canonical(t, null)),
		fingerprint.Fingerprint(canonical(t, absent)),
		fingerprint.Fingerprint(canonical(t, empty)),
	}

	slices.Sort(prints)

	if len(slices.Compact(prints)) != 3 {
		t.Fatalf("null, absent and empty share a fingerprint: %v", prints)
	}
}

func TestUnknownValuesAreReportedWithTheirAddresses(t *testing.T) {
	t.Parallel()

	payload := planPayload(t, `"resource_changes":[
	  {"address":"terraform_data.app","change":{
	     "after":{"input":"x"},
	     "after_unknown":{"id":true,"input":false,"output":true}}}],
	  "output_changes":{"tier":{"after_unknown":true}}`)

	unknowns := fingerprint.Unknowns(canonical(t, payload))

	want := []string{"output.tier", "terraform_data.app.id", "terraform_data.app.output"}
	if !slices.Equal(unknowns, want) {
		t.Fatalf("unknown addresses = %v, want %v", unknowns, want)
	}
}

func TestAVolatileTemplateKeepsItsStableComponents(t *testing.T) {
	t.Parallel()

	// The R2-9 shape: a value whose prefix moves every run and whose suffix is
	// the assertable part a whole-value mask would erase. Component granularity
	// comes from the syntax, which is where the impure subcomponent is visible.
	const path = "resource_changes[a.b].change.after.token"

	mask := fingerprint.NewMask()
	mask.Spans[path] = fingerprint.SyntaxSpan("", "-stable", false)

	first := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"token":"aaaaaaaa-stable"}}}]`))

	repeat := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"token":"cccccccc-stable"}}}]`))
	if delta := fingerprint.Compare(mask, first, repeat); !delta.Empty() {
		t.Fatalf("the volatile prefix was not masked: %+v", delta.Changes)
	}

	mutated := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"token":"dddddddd-stabl3"}}}]`))

	delta := fingerprint.Compare(mask, first, mutated)
	if delta.Empty() {
		t.Fatal("mutating the stable suffix of a volatile template produced no delta")
	}

	if !strings.Contains(delta.Changes[0].Mutant, "-stabl3") {
		t.Fatalf("the delta does not carry the mutated suffix: %+v", delta.Changes[0])
	}

	if got := delta.Addresses(); !slices.Equal(got, []string{"a.b.token"}) {
		t.Fatalf("delta addresses = %v, want the mutated attribute", got)
	}
}

func TestASpanFromTheSyntaxSurvivesAWholeMaskFromTheRuns(t *testing.T) {
	t.Parallel()

	const path = "resource_changes[a.b].change.after.token"

	observed := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"token":"aaaaaaaa-stable"}}}]`))
	moved := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"token":"bbbbbbbb-stable"}}}]`))

	// Two observations can only say the whole value moved.
	fromRuns := fingerprint.Derive(observed, moved)
	if span := fromRuns.Spans[path]; !span.Whole {
		t.Fatalf("a run-derived span claimed component granularity: %+v", span)
	}

	fromSyntax := fingerprint.NewMask()
	fromSyntax.Spans[path] = fingerprint.SyntaxSpan("", "-stable", false)

	merged := fromRuns.Merge(fromSyntax)
	if span := merged.Spans[path]; span.Whole || span.Suffix != "-stable" {
		t.Fatalf("the syntax-derived span did not survive the merge: %+v", span)
	}
}

func TestAWhollyVolatileValueIsMaskedWithoutPoisoningTheFingerprint(t *testing.T) {
	t.Parallel()

	first := canonical(t, payloadOf(t, tfexec.PayloadState,
		`{"state_format_version":"1.0","root_module":{"resources":[
		  {"address":"terraform_data.app","values":{"id":"7c13c363","input":"kept"}}]}}`))
	second := canonical(t, payloadOf(t, tfexec.PayloadState,
		`{"state_format_version":"1.0","root_module":{"resources":[
		  {"address":"terraform_data.app","values":{"id":"2efae1ab","input":"kept"}}]}}`))

	mask := fingerprint.Derive(first, second)

	third := canonical(t, payloadOf(t, tfexec.PayloadState,
		`{"state_format_version":"1.0","root_module":{"resources":[
		  {"address":"terraform_data.app","values":{"id":"c7a3dd4e","input":"kept"}}]}}`))

	delta := fingerprint.Compare(mask, first, third)
	if !delta.Empty() {
		t.Fatalf("a fully volatile identifier was not masked: %+v", delta)
	}

	if delta.Indeterminate {
		t.Fatal("a fully volatile value is a sound decomposition, not an undecidable one")
	}
}

func TestAShapeChangeBetweenBaselineRunsMakesTheComparisonIndeterminate(t *testing.T) {
	t.Parallel()

	first := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"input":"text"}}}]`))
	second := canonical(t, planPayload(t,
		`"resource_changes":[{"address":"a.b","change":{"after":{"input":7}}}]`))

	mask := fingerprint.Derive(first, second)

	delta := fingerprint.Compare(mask, first, first)
	if !delta.Indeterminate {
		t.Fatal("a value that changed type between baseline runs must not be treated as identical")
	}

	if delta.Empty() {
		t.Fatal("an indeterminate comparison is never fingerprint-identical")
	}
}
