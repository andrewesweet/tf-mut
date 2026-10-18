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
	"time"
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
		Files:       []FileTiming{},
		Started:     false,
		ExitCode:    exitCodeUnavailable,
		TimedOut:    false,
		Duration:    0,
	}

	clock := refNewStreamClock()

	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	for {
		if err := refDecodeMessage(decoder, &result, clock); err != nil {
			if errors.Is(err, io.EOF) {
				return result, nil
			}

			return result, fmt.Errorf("%w: %w", ErrMalformedStream, err)
		}
	}
}

type refStreamClock struct {
	runStarts  map[string]time.Time
	fileStarts map[string]time.Time
}

func refNewStreamClock() *refStreamClock {
	return &refStreamClock{runStarts: map[string]time.Time{}, fileStarts: map[string]time.Time{}}
}

func (c *refStreamClock) rememberRun(file, run string, at *time.Time) {
	if at != nil {
		c.runStarts[file+"\x00"+run] = *at
	}
}

func (c *refStreamClock) rememberFile(file string, at *time.Time) {
	if at != nil {
		c.fileStarts[file] = *at
	}
}

func (c *refStreamClock) runElapsed(file, run string, completed *time.Time) *int64 {
	start := c.runStarts[file+"\x00"+run]
	return refElapsedBetween(start, completed)
}

func (c *refStreamClock) fileElapsed(file string, completed *time.Time) *int64 {
	start := c.fileStarts[file]
	return refElapsedBetween(start, completed)
}

func refElapsedBetween(start time.Time, completed *time.Time) *int64 {
	if completed == nil || start.IsZero() {
		return nil
	}

	elapsed := completed.Sub(start).Milliseconds()

	return &elapsed
}

type refMessage struct {
	file      string
	run       string
	timestamp *time.Time
	outcome   *RunOutcome
	payload   *RunPayload
	problem   *Diagnostic
	timing    *FileTiming
}

func refDecodeMessage(decoder *json.Decoder, result *TestResult, clock *refStreamClock) error {
	if err := refExpectDelim(decoder, '{'); err != nil {
		return err
	}

	found := refMessage{}

	for decoder.More() {
		key, err := refReadKey(decoder)
		if err != nil {
			return err
		}

		if err := refDecodeMember(decoder, key, &found, clock); err != nil {
			return err
		}
	}

	if err := refExpectDelim(decoder, '}'); err != nil {
		return err
	}

	refAttach(found, result)

	return nil
}

func refDecodeMember(decoder *json.Decoder, key string, found *refMessage, clock *refStreamClock) error {
	switch key {
	case fieldTestFile:
		return decoder.Decode(&found.file)
	case fieldTestRun:
		return decoder.Decode(&found.run)
	case fieldTimestamp:
		return refDecodeTimestamp(decoder, found)
	case messageTestFile:
		return refDecodeFileTiming(decoder, found, clock)
	case messageTestRun:
		return refDecodeRunOutcome(decoder, found, clock)
	case messageDiagnostic:
		problem := Diagnostic{} // decoded from the stream.
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

func refDecodeTimestamp(decoder *json.Decoder, found *refMessage) error {
	raw := "" // decoded from the stream.
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil //nolint:nilerr // an unparseable timestamp is a timing gap, not a malformed stream.
	}

	found.timestamp = &parsed

	return nil
}

func refDecodeFileTiming(decoder *json.Decoder, found *refMessage, clock *refStreamClock) error {
	event := testFileMessage{} // decoded from the stream.
	if err := decoder.Decode(&event); err != nil {
		return err
	}

	switch event.Progress {
	case progressStarting:
		clock.rememberFile(event.Path, found.timestamp)
	case progressDone:
		elapsed := clock.fileElapsed(event.Path, found.timestamp)
		found.timing = &FileTiming{File: event.Path, ElapsedMS: elapsed}
	}

	return nil
}

func refDecodeRunOutcome(decoder *json.Decoder, found *refMessage, clock *refStreamClock) error {
	run := testRunMessage{} // decoded from the stream.
	if err := decoder.Decode(&run); err != nil {
		return err
	}

	if run.Progress == progressStarting {
		clock.rememberRun(run.Path, run.Run, found.timestamp)

		return nil
	}

	if run.Progress != progressDone {
		return nil
	}

	found.outcome = &RunOutcome{
		File:      run.Path,
		Run:       run.Run,
		Status:    run.Status,
		ElapsedMS: clock.runElapsed(run.Path, run.Run, found.timestamp),
	}

	return nil
}

func refDecodePayload(decoder *json.Decoder, kind string, found *refMessage) error {
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

	found.payload = &RunPayload{File: "", Run: "", Kind: kind, Value: value}

	return nil
}

func refAttach(found refMessage, result *TestResult) {
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

	if found.timing != nil {
		timing := *found.timing
		for _, run := range result.Runs {
			if run.File == timing.File {
				timing.Runs++
			}
		}

		result.Files = append(result.Files, timing)
	}
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

// assertSameDecode asserts that the production decoder and the frozen
// token-loop decoder agree on one input: both fail, both with the malformed
// class, or both succeed with identical events.
func assertSameDecode(tb testing.TB, input []byte, origin string) {
	tb.Helper()

	newResult, newErr := decodeTestStream(bytes.NewReader(input))
	oldResult, oldErr := refDecodeTestStream(bytes.NewReader(input))

	if (newErr == nil) != (oldErr == nil) {
		tb.Fatalf("%s: new error = %v, old error = %v", origin, newErr, oldErr)
	}

	if newErr != nil {
		if !errors.Is(newErr, ErrMalformedStream) || !errors.Is(oldErr, ErrMalformedStream) {
			tb.Fatalf("%s: error class mismatch: new %v, old %v", origin, newErr, oldErr)
		}

		return
	}

	if !reflect.DeepEqual(newResult, oldResult) {
		tb.Fatalf("%s: decoded events differ:\nnew: %#v\nold: %#v", origin, newResult, oldResult)
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

// Every way an input can end around the skipped value. Between tokens the
// token loop reads the end of input as the end of the stream; inside a token
// it reports an unexpected end. The filter classifies an end of input the same
// way, and every case here pins one line of that table.
func TestOldAndNewAgreeOnEndOfInputAroundTheSchemaValue(t *testing.T) {
	t.Parallel()

	const head = `{"@level":"info","test_plan":{"provider_schemas":`

	for name, value := range map[string]string{
		"after colon":        "",
		"after key":          `"provider_schemas"`,
		"mid key":            `{"provider_sch`,
		"open object":        `{`,
		"after inner key":    `{"a"`,
		"mid number":         `123`,
		"after number":       `123}`,
		"mid negation":       `-`,
		"mid fraction":       `12.`,
		"after fraction":     `12.5`,
		"mid exponent":       `12e`,
		"mid exponent sign":  `12e+`,
		"after exponent":     `12e3`,
		"mid literal":        `tru`,
		"whole literal":      `true`,
		"mid string":         `"ab`,
		"mid escape":         `{"a":"x\`,
		"mid unicode escape": `{"a":"x\u0`,
		"open array":         `[`,
		"array one element":  `[1`,
		"after comma":        `[1,`,
		"array two elements": `[1,2`,
		"closed array":       `[1,2]`,
		"nested open object": `{"a":{`,
		"nested complete":    `{"a":1}`,
		// A matched key whose separator never arrives: the token loop reads the
		// next string as a scalar value and then as a key, so a later colon can
		// still open a swallow. The filter's key gate must stay aligned with
		// that reading all the way to the verdict.
		"key then second key":      `"provider_schemas" "provider_schemas":{"aws":1}}`,
		"key then key then key":    `"provider_schemas" "provider_schemas" "provider_schemas":{"aws":1}}`,
		"key then object":          `"provider_schemas" {"aws":1}}`,
		"key then object with key": `"provider_schemas" {"provider_schemas":1}}`,
		"key then scalar":          `"provider_schemas" 1}}`,
		"key then string":          `"provider_schemas" "x"}}`,
		"key then malformed array": `"provider_schemas" [,1]}}`,
	} {
		assertSameDecode(t, []byte(head+value), "end of input "+name)
	}
}

// A schema at the scale the skip targets — the synthetic analogue of a real
// provider registry download — drives both decoders in this benchmark; the
// equivalence tests above guarantee they decode it identically.
func BenchmarkTheSchemaSkip(b *testing.B) {
	var schemas bytes.Buffer

	schemas.WriteString(`{"aws":{`)

	for range 512 {
		schemas.WriteString(`"Microsoft.Compute/virtualMachines":{"block":{`)

		for i := range 64 {
			fmt.Fprintf(&schemas, `"attribute_%d":{"type":"string","description":`+
				`"braces } brackets ] quotes \" backslash \\ and a long body"},`, i)
		}

		schemas.WriteString(`"tail":{"type":"number","optional":true}}},`)
	}

	schemas.WriteString(`"final":{"block":{}}}}`)

	stream := payloadMessage(schemas.String())

	want := []RunPayload{{
		File: "tests/unit.tftest.hcl",
		Run:  "one",
		Kind: PayloadPlan,
		Value: map[string]any{
			"planned_values": map[string]any{
				"outputs": map[string]any{
					"tier": map[string]any{"value": "standard"},
				},
			},
			"configuration": map[string]any{
				"root_module": map[string]any{"outputs": map[string]any{}},
			},
		},
	}}

	for name, decode := range map[string]func(io.Reader) (TestResult, error){
		"token loop":  refDecodeTestStream,
		"byte filter": decodeTestStream,
	} {
		b.Run(name, func(b *testing.B) {
			result, err := decode(bytes.NewReader(stream))
			if err != nil {
				b.Fatal(err)
			}

			if !reflect.DeepEqual(result.Payloads, want) {
				b.Fatalf("decoded payloads = %#v, want %#v", result.Payloads, want)
			}

			b.SetBytes(int64(len(stream)))
			b.ReportAllocs()

			for b.Loop() {
				if _, err := decode(bytes.NewReader(stream)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// FuzzTheByteSkipAgreesWithTheTokenLoop drives both decoders with arbitrary
// bytes: the filter may never produce a stream the token loop would have
// decoded differently. Run with -fuzz for the search; under go test the seed
// corpus alone runs, keeping the suite deterministic and offline.
func FuzzTheByteSkipAgreesWithTheTokenLoop(f *testing.F) {
	f.Add(recorded(f))
	f.Add(payloadMessage(`{}`))
	f.Add(payloadMessage(`{"aws":{"provider":{"version":0,"block":{"a":"b \" ] }"}},` +
		`"resource_schemas":{"aws_instance":{"block":{"x":[1,2,{"y":[true,false,null,1.5e-3]}]}}}}`))
	f.Add(payloadMessage(`{"a":{"b":{"c":{"d":{"e":[[[["x"]]]]}}}}}`))
	f.Add(payloadMessage(`{"aws":"strings with } ] \" \\ and unicode \u00e9"}`))
	f.Add(payloadMessage(`{"aws" 1}`))
	f.Add(payloadMessage(`{"aws":12xy}`))
	f.Add(payloadMessage(`12345`))
	f.Add(payloadMessage(`true`))
	f.Add([]byte(`{"@level":"info","test_plan":{"provider_schemas":{"aws":[1,2`))

	f.Fuzz(func(t *testing.T, data []byte) {
		assertSameDecode(t, data, "fuzzed input")
	})
}
