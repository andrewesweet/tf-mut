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

const (
	identityMarker      = "output_changes.identity.after_unknown"
	identityValue       = "outputs.identity.value"
	identityChangeAfter = "output_changes.identity.after"
)

// The metadata members of an output record are never assertable, and a
// candidate selector has to tell them from the value delta without rebuilding
// the path grammar.
func TestOutputMetadataPathsAreRecognisedWithoutRebuildingTheGrammar(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"outputs.identity.type",
		"outputs.identity.type[0]",
		"outputs.identity.sensitive",
		identityMarker,
		"output_changes.identity.after_unknown.principal_id",
		"output_changes.identity.after_sensitive",
		"output_changes.identity.before_sensitive",
	} {
		if !fingerprint.ParsePath(path).IsOutputMetadata() {
			t.Fatalf("IsOutputMetadata(%q) = false, want true", path)
		}
	}

	for _, path := range []string{
		identityValue,
		"outputs.identity.value.principal_id",
		"outputs.region_list.value[0].short_name",
		identityChangeAfter,
		"output_changes.identity.after.principal_id",
		"outputs.after.value",
		"root_module.resources[example_thing.app].values.input",
		"resource_changes[example_thing.app].change.after.input",
	} {
		if fingerprint.ParsePath(path).IsOutputMetadata() {
			t.Fatalf("IsOutputMetadata(%q) = true, want false", path)
		}
	}
}

// Split's contract is about payload paths, so the shapes are stated directly
// here: state paths carry the output body under `value`, plan paths under
// `after`, and both may continue past the body into an element of the output's
// collection — `outputs.X.value[0].attr` and `output_changes.X.after[0].attr`
// are what Terraform v1.15.8 emits for list-valued outputs.
func TestSplitCarriesAnOutputElementPathPastItsBody(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path      string
		resource  string
		attribute string
	}{
		{
			"outputs.region_list.value[0].short_name",
			"output.region_list", "[0].short_name",
		},
		{
			"output_changes.region_list.after[1].short_name",
			"output.region_list", "[1].short_name",
		},
		{
			identityValue,
			"output.identity", "",
		},
		{
			"outputs.identity.value.tenant",
			"output.identity", "tenant",
		},
		{
			"outputs.identity.type[0]",
			"", "",
		},
		{
			"outputs.identity.sensitive",
			"", "",
		},
		{
			identityMarker,
			"", "",
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			resource, attribute, ok := fingerprint.ParsePath(testCase.path).Split()
			if testCase.resource == "" {
				if ok {
					t.Fatalf("Split(%q) = (%q, %q, true), want not ok",
						testCase.path, resource, attribute)
				}
				return
			}

			if !ok || resource != testCase.resource || attribute != testCase.attribute {
				t.Fatalf("Split(%q) = (%q, %q, %v), want (%q, %q, true)",
					testCase.path, resource, attribute, ok,
					testCase.resource, testCase.attribute)
			}
		})
	}
}

// TestAnUnknownMarkerPathMapsOntoTheValueItMarks pins the mapping between a
// plan's unknown-marker record and the value whose parts it marks, for the
// record itself, a leaf under it, and an element inside a collection under it.
// Every verdict the mapping leads to is asserted through the engine seam; the
// shapes here are documents' contracts, taken from recorded real output.
func TestAnUnknownMarkerPathMapsOntoTheValueItMarks(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		marker string
		value  string
	}{
		{
			identityMarker,
			identityChangeAfter,
		},
		{
			"output_changes.identity.after_unknown.principal_id",
			"output_changes.identity.after.principal_id",
		},
		{
			"output_changes.identity.after_unknown.items[0].port",
			"output_changes.identity.after.items[0].port",
		},
		{
			// A plan whose whole element is unknown marks the member under the
			// element's index: the marker names after[0].id, not after.id.
			"output_changes.identity.after_unknown[0].id",
			"output_changes.identity.after[0].id",
		},
		{
			"resource_changes[null_resource.app].change.after_unknown.triggers",
			"resource_changes[null_resource.app].change.after.triggers",
		},
		{
			"resource_changes[null_resource.app].change.after_unknown[1].triggers",
			"resource_changes[null_resource.app].change.after[1].triggers",
		},
	} {
		t.Run(testCase.marker, func(t *testing.T) {
			t.Parallel()

			if !fingerprint.ParsePath(testCase.marker).IsUnknownMarker() {
				t.Fatalf("IsUnknownMarker(%q) = false, want true", testCase.marker)
			}

			value, found := fingerprint.ParsePath(testCase.marker).UnknownValueBase()
			if !found || value != testCase.value {
				t.Fatalf("UnknownValueBase(%q) = (%q, %v), want (%q, true)",
					testCase.marker, value, found, testCase.value)
			}
		})
	}
}

// TestNonMarkerPathsNameNoMarkerRecord keeps the mapping from widening: a
// value path, a sensitivity record, the before half of a change, and an
// output whose own name embeds the marker's field name are none of them
// unknown markers.
func TestNonMarkerPathsNameNoMarkerRecord(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		identityChangeAfter,
		"output_changes.identity.after_sensitive",
		"output_changes.identity.after_unknownish.children",
		"output_changes.identity.after_unknown[0",
		"resource_changes[null_resource.app].change.before",
		"resource_changes[null_resource.app].change.after.triggers",
		"outputs.after_unknown",
		"root_module.resources[null_resource.app].values.triggers",
	} {
		if fingerprint.ParsePath(path).IsUnknownMarker() {
			t.Fatalf("IsUnknownMarker(%q) = true, want false", path)
		}
	}
}

// TestACountedInstancePathNamesItsResourceAndIndex pins the instance grammar
// the count evidence reads: state and plan resource paths with numeric
// instance indices name their resource; singletons, for_each instances and
// everything outside a resource do not.
func TestACountedInstancePathNamesItsResourceAndIndex(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path     string
		resource string
		index    int
	}{
		{
			"root_module.resources[null_resource.app[1]].values.triggers",
			"null_resource.app", 1,
		},
		{
			"resource_changes[terraform_data.counted[0]].change.after.output",
			"terraform_data.counted", 0,
		},
		{
			"resource_changes[module.child.terraform_data.inner[12]].change.after",
			"module.child.terraform_data.inner", 12,
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			resource, index, ok := fingerprint.ParsePath(testCase.path).CountedInstance()
			if !ok || resource != testCase.resource || index != testCase.index {
				t.Fatalf("CountedInstance(%q) = (%q, %d, %v), want (%q, %d, true)",
					testCase.path, resource, index, ok, testCase.resource, testCase.index)
			}
		})
	}
}

// TestPathsOutsideTheInstanceGrammarNameNoInstance keeps the count evidence
// from widening: singletons and string-keyed for_each instances carry no
// count index, and paths outside any resource name nothing.
func TestPathsOutsideTheInstanceGrammarNameNoInstance(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"root_module.resources[null_resource.app].values.triggers",
		`root_module.resources[null_resource.app["blue"]].values.triggers`,
		"root_module.resources[null_resource.app[blue]].values.triggers",
		identityChangeAfter,
		identityValue,
		"resource_changes[null_resource.app].change.after",
	} {
		if _, _, ok := fingerprint.ParsePath(path).CountedInstance(); ok {
			t.Fatalf("CountedInstance(%q) reported an instance, want none", path)
		}
	}
}

// TestAParsedPathProjectsTheAddressADiagnosisNames pins the address projection
// over the four records the grammar names and the members it does not. The
// shapes are the recorded ones the tables above and the suggest fixtures carry.
func TestAParsedPathProjectsTheAddressADiagnosisNames(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path    string
		address string
	}{
		{
			"resource_changes[null_resource.app].change.after.triggers",
			"null_resource.app.triggers",
		},
		{
			"root_module.resources[example_thing.app].values.input",
			"example_thing.app.input",
		},
		{
			"outputs.region_list.value[0].short_name",
			"output.region_list",
		},
		{
			"output_changes.identity.after",
			"output.identity",
		},
		{
			"resource_changes[a.b].change.after.c",
			"a.b.c",
		},
		{
			// A child module's state names no address an assertion could read.
			"root_module.child_modules[0].resources[A].values.x",
			"",
		},
		{
			"format_version",
			"",
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			if address := fingerprint.ParsePath(testCase.path).Address(); address != testCase.address {
				t.Fatalf("Address(%q) = %q, want %q", testCase.path, address, testCase.address)
			}
		})
	}
}

// TestTheAttributeQuestionIsAnsweredAgainstTheParsedOwner pins the attribute
// question the mask asks: true for the attribute itself and for anything
// beneath it, false across bodies, addresses and records.
func TestTheAttributeQuestionIsAnsweredAgainstTheParsedOwner(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path      string
		address   string
		attribute string
		want      bool
	}{
		{
			"resource_changes[null_resource.app].change.after.triggers",
			"null_resource.app", "triggers", true,
		},
		{
			"resource_changes[null_resource.app].change.after.triggers.x",
			"null_resource.app", "triggers", true,
		},
		{
			// The before body is a different argument of the same resource.
			"resource_changes[null_resource.app].change.before",
			"null_resource.app", "triggers", false,
		},
		{
			"root_module.resources[example_thing.app].values.input",
			"example_thing.app", "input", true,
		},
		{
			// An output record answers neither projection of the question.
			"outputs.identity.value",
			"null_resource.app", "triggers", false,
		},
		{
			"resource_changes[example_thing.app].change.after.input",
			"null_resource.app", "triggers", false,
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			parsed := fingerprint.ParsePath(testCase.path)
			if got := parsed.Attribute(testCase.address, testCase.attribute); got != testCase.want {
				t.Fatalf("Attribute(%q, %q, %q) = %v, want %v",
					testCase.path, testCase.address, testCase.attribute, got, testCase.want)
			}
		})
	}
}

// TestSegmentsSplitTheAttributeOnTopLevelDots pins the segment split the
// renderers consume, including the element key that stays attached to the
// segment it indexes and the path that names no assertable value.
func TestSegmentsSplitTheAttributeOnTopLevelDots(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path     string
		segments []string
	}{
		{
			"outputs.region_list.value[0].short_name",
			[]string{"[0]", "short_name"},
		},
		{
			"root_module.resources[null_resource.app].values.triggers",
			[]string{"triggers"},
		},
		{
			"resource_changes[a.b].change.after.c",
			[]string{"c"},
		},
		{
			"output_changes.identity.after_unknown",
			nil,
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			segments := fingerprint.ParsePath(testCase.path).Segments()
			if !slices.Equal(segments, testCase.segments) {
				t.Fatalf("Segments(%q) = %q, want %q", testCase.path, segments, testCase.segments)
			}
		})
	}
}

// TestACountedInstanceRootSpellsTheInstanceRecord pins the root spelling the
// count evidence reads, on both records and on paths that name no bracket at
// all — the answer is the spelling's own, bracket for bracket.
func TestACountedInstanceRootSpellsTheInstanceRecord(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		path string
		root string
	}{
		{
			"root_module.resources[null_resource.app[1]].values.triggers",
			"root_module.resources[null_resource.app[1]]",
		},
		{
			"resource_changes[terraform_data.counted[0]].change.after.output",
			"resource_changes[terraform_data.counted[0]]",
		},
		{
			"outputs.region_list.value[0].short_name",
			"outputs.region_list.value[0]",
		},
		{
			"output_changes.identity.after",
			"]",
		},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			if root := fingerprint.ParsePath(testCase.path).CountedInstanceRoot(); root != testCase.root {
				t.Fatalf("CountedInstanceRoot(%q) = %q, want %q", testCase.path, root, testCase.root)
			}
		})
	}
}
