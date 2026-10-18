//nolint:testpackage // the equivalence oracle must reach the decoder's unexported event types.
package tfexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The byte-level provider_schemas skip must not change what the decoder
// produces. The oracle for that claim is the previous decoder itself: the
// token-loop implementation below is a frozen copy of the pre-L3
// decodeTestStream, kept so that the production decoder can be compared
// against the behaviour it replaced on arbitrary input — recorded streams,
// synthetic edge cases, every truncation of both, and fuzzed bytes.

func refDecodeTestStream(reader io.Reader) (TestResult, error) {
	result := TestResult{
		Runs:        []RunOutcome{},
		Diagnostics: []Diagnostic{},
		Payloads:    []RunPayload{},
		Started:     false,
		ExitCode:    exitCodeUnavailable,
		TimedOut:    false,
		Duration:    0,
	}

	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	for {
		if err := refDecodeMessage(decoder, &result); err != nil {
			if errors.Is(err, io.EOF) {
				return result, nil
			}

			return result, fmt.Errorf("%w: %w", ErrMalformedStream, err)
		}
	}
}

func refDecodeMessage(decoder *json.Decoder, result *TestResult) error {
	if err := refExpectDelim(decoder, '{'); err != nil {
		return err
	}

	found := message{}

	for decoder.More() {
		key, err := refReadKey(decoder)
		if err != nil {
			return err
		}

		if err := refDecodeMember(decoder, key, &found); err != nil {
			return err
		}
	}

	if err := refExpectDelim(decoder, '}'); err != nil {
		return err
	}

	refAttach(found, result)

	return nil
}

func refDecodeMember(decoder *json.Decoder, key string, found *message) error {
	switch key {
	case fieldTestFile:
		return decoder.Decode(&found.file)
	case fieldTestRun:
		return decoder.Decode(&found.run)
	case messageTestRun:
		run := testRunMessage{}
		if err := decoder.Decode(&run); err != nil {
			return err
		}

		if run.Progress != progressDone {
			return nil
		}

		found.outcome = &RunOutcome{File: run.Path, Run: run.Run, Status: run.Status}

		return nil
	case messageDiagnostic:
		problem := Diagnostic{}
		if err := decoder.Decode(&problem); err != nil {
			return err
		}

		found.problem = &problem

		return nil
	case messageTestPlan:
		return refDecodePayload(decoder, PayloadPlan, found)
	case messageTestState:
		return refDecodePayload(decoder, PayloadState, found)
	default:
		return refSkipValue(decoder)
	}
}

func refDecodePayload(decoder *json.Decoder, kind string, found *message) error {
	if err := refExpectDelim(decoder, '{'); err != nil {
		return err
	}

	value := map[string]any{}

	for decoder.More() {
		key, err := refReadKey(decoder)
		if err != nil {
			return err
		}

		if key == fieldProviders {
			if err := refSkipValue(decoder); err != nil {
				return err
			}

			continue
		}

		member := any(nil)
		if err := decoder.Decode(&member); err != nil {
			return err
		}

		value[key] = member
	}

	if err := refExpectDelim(decoder, '}'); err != nil {
		return err
	}

	found.payload = &RunPayload{Kind: kind, Value: value}

	return nil
}

func refSkipValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}

	depth := 1
	if delim == '}' || delim == ']' {
		return fmt.Errorf("%w: unexpected %s", ErrMalformedStream, delim)
	}

	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}

		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
		}
	}

	return nil
}

func refReadKey(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}

	key, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%w: object key is %T", ErrMalformedStream, token)
	}

	return key, nil
}

func refExpectDelim(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok || delim != want {
		return fmt.Errorf("%w: expected %s, found %v", ErrMalformedStream, want, token)
	}

	return nil
}

func refAttach(found message, result *TestResult) {
	if found.outcome != nil {
		result.Runs = append(result.Runs, *found.outcome)
	}

	if found.problem != nil {
		problem := *found.problem
		problem.TestFile = found.file
		problem.TestRun = found.run
		result.Diagnostics = append(result.Diagnostics, problem)
	}

	if found.payload != nil {
		payload := *found.payload
		payload.File = found.file
		payload.Run = found.run
		result.Payloads = append(result.Payloads, payload)
	}
}

// assertSameDecode asserts that the production decoder and the frozen
// token-loop decoder agree on one input: both fail, both with the malformed
// class, or both succeed with identical events.
func assertSameDecode(t *testing.T, input []byte, origin string) {
	t.Helper()

	newResult, newErr := decodeTestStream(bytes.NewReader(input))
	oldResult, oldErr := refDecodeTestStream(bytes.NewReader(input))

	if (newErr == nil) != (oldErr == nil) {
		t.Fatalf("%s: new error = %v, old error = %v", origin, newErr, oldErr)
	}

	if newErr != nil {
		if !errors.Is(newErr, ErrMalformedStream) || !errors.Is(oldErr, ErrMalformedStream) {
			t.Fatalf("%s: error class mismatch: new %v, old %v", origin, newErr, oldErr)
		}

		return
	}

	if !reflect.DeepEqual(newResult, oldResult) {
		t.Fatalf("%s: decoded events differ:\nnew: %#v\nold: %#v", origin, newResult, oldResult)
	}
}

// A payload message with the given provider_schemas value and two real
// neighbouring members, so that every test exercises the decoder's position
// after the skipped value.
func payloadMessage(schemas string) []byte {
	return []byte(`{"@level":"info","@testfile":"tests/unit.tftest.hcl","@testrun":"one",` +
		`"test_plan":{"planned_values":{"outputs":{"tier":{"value":"standard"}}},` +
		`"provider_schemas":` + schemas + `,` +
		`"configuration":{"root_module":{"outputs":{}}}}}` + "\n")
}

func TestOldAndNewDecodeTheRecordedStreamIdentically(t *testing.T) {
	t.Parallel()

	assertSameDecode(t, recorded(t), "recorded stream")
}

// The memory gate in stream_test.go already drives the eight-megabyte
// inflation through the decoder; the equivalence oracle runs the same shape at
// a size that keeps the suite fast.
func TestOldAndNewDecodeAnInflatedStreamIdentically(t *testing.T) {
	t.Parallel()

	assertSameDecode(t, inflatedBy(t, recorded(t), 1024*1024), "inflated stream")
}

func TestOldAndNewAgreeAtEveryTruncationOfTheRecordedStream(t *testing.T) {
	t.Parallel()

	stream := recorded(t)

	for cut := range stream {
		if cut > 2048 && cut%53 != 0 {
			continue
		}

		assertSameDecode(t, stream[:cut], fmt.Sprintf("recorded stream truncated to %d bytes", cut))
	}
}

func TestOldAndNewAgreeAtTruncationsInsideAnInflatedSchema(t *testing.T) {
	t.Parallel()

	stream := inflate(recorded(t))

	// The inflated stream is megabytes long; cutting at a spread of offsets
	// inside the first provider_schemas value covers the swallow's chunk
	// boundaries without repeating the whole megabyte range.
	cuts := make([]int, 0, 13)
	cuts = append(cuts, len(stream)/1024, len(stream)/512, len(stream)/256, len(stream)/128)
	marker := bytes.Index(stream, []byte(`"filler":[`))

	if marker < 0 {
		t.Fatal("the inflated stream carries no filler member")
	}

	for _, offset := range []int{1, 17, 63, 64, 65, 1023, 1024, 1025, 4096} {
		cuts = append(cuts, marker+offset)
	}

	for _, cut := range cuts {
		assertSameDecode(t, stream[:cut], fmt.Sprintf("inflated stream truncated to %d bytes", cut))
	}
}

func TestOldAndNewDecodeEdgeCaseSchemasIdentically(t *testing.T) {
	t.Parallel()

	for name, schemas := range map[string]string{
		"empty object":          `{}`,
		"empty array":           `[]`,
		"nested arrays":         `[[1,[2,[3,[4]]]],{"k":[true,null,-1.5e-10]}]`,
		"strings with braces":   `{"aws":{"block":{"description":"braces } and brackets ] and commas , inside"}}}`,
		"escaped quotes":        `{"aws":{"description":"a \"quoted\" word"}}`,
		"escaped backslashes":   `{"aws":{"description":"backslash \\ then quote \" then brace }"}}`,
		"unicode escapes":       `{"aws":{"description":"\u0041\u00e9\u4e2d"}}`,
		"control-like escapes":  `{"aws":{"description":"line\nbreak\ttab"}}`,
		"numbers":               `{"a":{"v":0},"b":{"v":-0.5},"c":{"v":1e+30},"d":{"v":-12.75E-3},"e":{"v":12345}}`,
		"literals":              `{"a":true,"b":false,"c":null}`,
		"schema-like strings":   `{"aws":{"description":"the member \"provider_schemas\": appears in this text"}}`,
		"deep nesting":          `{"a":{"b":{"c":{"d":{"e":[[[["x"]]]]}}}}}`,
		"whitespace":            "{ \"aws\" : { \"block\" : { } } }",
		"scalar null":           `null`,
		"scalar string":         `"text"`,
		"scalar number":         `42`,
		"scalar true":           `true`,
		"prefix-extended key":   `{"provider_schemas_extended":{"real":"value"}}`,
		"nested key at depth 3": `{"outer":{"provider_schemas":{"real":"value"}}}`,
		"array element text":    `{"addresses":["provider_schemas",{"x":1}]}`,
		"string in the value":   `{"aws":{"description":"provider_schemas"}}`,
	} {
		assertSameDecode(t, payloadMessage(schemas), "schema "+name)
	}
}

func TestOldAndNewDecodeEdgeCasePlacementsIdentically(t *testing.T) {
	t.Parallel()

	for name, stream := range map[string]string{
		"first member": `{"@level":"info","test_plan":{"provider_schemas":{"aws":{}},"planned_values":{}}}`,
		"last member":  `{"@level":"info","test_plan":{"planned_values":{},"provider_schemas":{"aws":{}}}}`,
		"only member":  `{"@level":"info","test_plan":{"provider_schemas":{"aws":{}}}}`,
		"empty value":  `{"@level":"info","test_plan":{"provider_schemas":{},"planned_values":{}}}`,
		"duplicate keys": `{"@level":"info","test_plan":` +
			`{"provider_schemas":{"a":{}},"provider_schemas":{"b":{}},"planned_values":{}}}`,
		"message-level key": `{"provider_schemas":{"a":{}},"type":"test_plan"}`,
		"state payload":     `{"@level":"info","test_state":{"provider_schemas":{"aws":{}},"root_module":{}}}`,
		"between other messages": `{"type":"version","terraform":"1.15.8"}` + "\n" +
			`{"@testfile":"f","@testrun":"r","test_plan":{"provider_schemas":{"aws":{}}}}` + "\n" +
			`{"type":"test_run","test_run":{"path":"f","run":"r","progress":"complete","status":"pass"}}` + "\n",
		"no schemas at all": `{"type":"version","terraform":"1.15.8"}` + "\n",
	} {
		assertSameDecode(t, []byte(stream), "placement "+name)
	}
}

func TestOldAndNewAgreeOnMalformedSchemas(t *testing.T) {
	t.Parallel()

	for name, schemas := range map[string]string{
		"truncated object":      `{"aws":{`,
		"truncated array":       `{"aws":[1,`,
		"missing value":         `{"aws":}`,
		"missing colon":         `{"aws" 1}`,
		"bad literal":           `{"aws":tru}`,
		"bad escape":            `{"aws":"bad \q escape"}`,
		"bare control char":     "{\"aws\":\"line\nbreak\"}",
		"truncated escape":      `{"aws":"unicode \u00"}`,
		"leading zero":          `{"aws":[01]}`,
		"dangling minus":        `{"aws":-}`,
		"trailing comma":        `{"aws":[1,]}`,
		"single quotes":         `{'aws':1}`,
		"unquoted key":          `{aws:1}`,
		"mismatched closer":     `{"aws":[1}}`,
		"number then garbage":   `{"aws":12xy}`,
		"exponent then garbage": `{"aws":1e+}`,
	} {
		assertSameDecode(t, payloadMessage(schemas), "malformed schema "+name)
	}
}

// The byte-level skip is worth nothing unless the large schemas it targets
// still decode identically, and the equivalence must cover a value that spans
// more than one read chunk.
func TestOldAndNewDecodeALargeSchemaIdentically(t *testing.T) {
	t.Parallel()

	var schemas bytes.Buffer

	schemas.WriteString(`{"azure":{`)

	for range 512 {
		schemas.WriteString(`"Microsoft.Compute/virtualMachines":{"block":{`)

		for i := range 64 {
			fmt.Fprintf(&schemas, `"attribute_%d":{"type":"string","description":`+
				`"braces } brackets ] quotes \" backslash \\ and a long body"},`, i)
		}

		schemas.WriteString(`"tail":{"type":"number","optional":true}}},`)
	}

	schemas.WriteString(`"final":{"block":{}}}}`)

	assertSameDecode(t, payloadMessage(schemas.String()), "large synthetic schema")
}

func TestTheSkipLeavesPayloadValuesIntact(t *testing.T) {
	t.Parallel()

	result, err := decodeTestStream(bytes.NewReader(recorded(t)))
	if err != nil {
		t.Fatalf("decoding the recorded stream: %v", err)
	}

	if len(result.Payloads) != 2 {
		t.Fatalf("payloads = %d, want 2", len(result.Payloads))
	}

	kinds := map[string]bool{}

	for _, payload := range result.Payloads {
		kinds[payload.Kind] = true

		if _, dropped := payload.Value[fieldProviders]; dropped {
			t.Fatalf("payload for %s kept %s", payload.Run, fieldProviders)
		}

		// A plan payload keeps its planned values and a state payload keeps its
		// state; either way the members around the skipped one survive.
		if len(payload.Value) == 0 {
			t.Fatalf("payload for %s kept nothing (%v)", payload.Run, payload.Value)
		}
	}

	if !kinds[PayloadPlan] || !kinds[PayloadState] {
		t.Fatalf("payload kinds = %v, want both", kinds)
	}
}

// inflatedBy rebuilds the recorded stream with each provider_schemas value
// grown to at least the given size, independent of the memory gate's inflate.
func inflatedBy(t *testing.T, stream []byte, size int) []byte {
	t.Helper()

	entry := `"` + strings.Repeat("f", 256) + `"`
	entries := make([]string, 0, size/(len(entry)+1))

	for range size / (len(entry) + 1) {
		entries = append(entries, entry)
	}

	filler := `"filler":[` + strings.Join(entries, ",") + `],`

	return bytes.ReplaceAll(stream, []byte(`"provider_schemas":{`), []byte(`"provider_schemas":{`+filler))
}

func TestOldAndNewAgreeOnAnInflatedStreamWithTheSharedFixture(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(recordedStream)
	if err != nil {
		t.Fatalf("reading %s: %v", recordedStream, err)
	}

	assertSameDecode(t, inflatedBy(t, content, 2*1024*1024), "2 MB inflated stream")
}
