package tfexec

import (
	"errors"
	"fmt"
	"io"
)

// The `provider_schemas` member of a verbose run payload carries each
// provider's whole schema — megabytes per run block against a large provider —
// and the decoder discards it unread. Tokenising those bytes with
// json.Decoder.Token dominated tf-mut's own CPU on large-schema suites.
//
// The skip cannot be a byte scanner run against the reader beside the decoder:
// json.Decoder buffers arbitrarily far ahead of its scan position, so the
// bytes between the decoder's position and its buffer end can be neither
// reclaimed nor replayed. Instead schemaFilter sits between the reader and the
// decoder and rewrites the stream in flight: a `"provider_schemas"` member at
// payload level has its value consumed byte-by-byte and replaced with `{}`
// before the decoder ever sees it. Decoder lookahead is harmless because the
// rewrite is applied exactly once, where the bytes flow — whatever the decoder
// has buffered is already the rewritten stream.
//
// The filter tracks brace and bracket depth and string state, including
// escapes, to find the value's end, and validates the value with the JSON
// grammar as it goes, so malformed input fails with the malformed-stream error
// the token loop produced, truncation produces no hang, and nothing beyond the
// value is consumed. Every other byte — including any `"provider_schemas"`
// that is not a payload-level object key — passes through untouched, and the
// decoder's own validation still governs the stream around the skipped value.
type schemaFilter struct {
	src io.Reader

	buf    []byte // the current chunk read from src
	bufEnd int    // bytes read into buf
	bufPos int    // next unconsumed byte of buf

	pending []byte // rewritten bytes awaiting delivery
	served  int    // drained prefix of pending

	srcErr error // a src error that arrived alongside data, surfaced once the data is spent

	err error // terminal error, sticky

	// Pass-through scan state.
	depth                int  // composite values open in the stream around the filter's gate
	inString             bool // inside a JSON string
	escaped              bool // the previous string byte was a backslash
	expectKey            bool // the next quoted string opens at object-key position at payload depth
	objectAtPayloadDepth bool // the depth-two container is an object, so its commas separate members

	state  schemaState
	keyPos int // progress through schemaKey while matching

	emptyRead int

	skip swallower
}

// schemaState is the pass-through machine's phase. Every state except the
// swallowed value emits each byte unchanged; the states only decide which
// bytes start a match and which start a swallow.
type schemaState int

const (
	stateScan  schemaState = iota // pass through, tracking structure for the gate
	stateKey                      // a payload-depth key-position string is being compared to schemaKey
	stateColon                    // the key matched; expecting whitespace and ':'
	stateValue                    // the colon matched; the next non-whitespace byte starts the value
)

// schemaKey is the exact byte pattern of the skipped member's name, quotes
// included. A key-position string at payload depth must match it byte for
// byte; anything else passes through.
const schemaKey = `"provider_schemas"`

// payloadDepth is the depth of the payload object inside a verbose message:
// `{"test_plan":{"provider_schemas":...}}` nests the member one brace below
// the message object. Only a key string at this depth opens a swallow.
const payloadDepth = 2

// initialPendingCapacity covers the common passes — chunks that need no
// rewrite or a single small substitution — without growing the slice.
const initialPendingCapacity = 256

// firstControlByte is the first printable ASCII byte; JSON strings refuse
// anything below it unescaped.
const firstControlByte = 0x20

// unicodeDigits is the length of the \uXXXX escape's hex tail.
const unicodeDigits = 4

// errSkipMalformedValue is the error the swallow reports for input the token
// loop would also have refused. decodeTestStream wraps it into the
// malformed-stream class, so its identity matters only to tests and readers
// of the message.
var errSkipMalformedValue = errors.New("malformed skipped provider_schemas value")

func newSchemaFilter(src io.Reader) *schemaFilter {
	return &schemaFilter{
		src:     src,
		buf:     make([]byte, streamBufferSize),
		keyPos:  -1,
		pending: make([]byte, 0, initialPendingCapacity),
	}
}

// Read hands the decoder the rewritten stream.
func (f *schemaFilter) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if f.served < len(f.pending) {
			n := copy(p, f.pending[f.served:])
			f.served += n

			if f.served == len(f.pending) {
				f.pending = f.pending[:0]
				f.served = 0
			}

			return n, nil
		}

		if f.err != nil {
			return 0, f.err
		}

		for f.bufPos < f.bufEnd {
			f.consume(f.buf[f.bufPos])
			f.bufPos++

			if f.err != nil {
				break
			}
		}

		if f.err == nil && f.bufPos >= f.bufEnd {
			f.fill()
		}
	}
}

// fill reads the next chunk from src and records, classified, any error it
// reports. An end of input is the stream's end unless the swallow is in the
// middle of a token, where the token loop would have reported an unexpected
// end rather than a clean one.
func (f *schemaFilter) fill() {
	if f.srcErr != nil {
		f.err = f.classifyEOF(f.srcErr)

		return
	}

	// Read is the only caller and only fills a drained buffer, so the window
	// resets to the start of the chunk.
	if f.bufPos == f.bufEnd {
		f.bufPos, f.bufEnd = 0, 0
	}

	for {
		n, err := f.src.Read(f.buf[f.bufEnd:])
		f.bufEnd += n

		if n > 0 {
			f.emptyRead = 0

			if err != nil {
				f.srcErr = err
			}

			return
		}

		if err != nil {
			f.emptyRead = 0
			f.err = f.classifyEOF(err)

			return
		}

		// A reader that keeps returning (0, nil) must not spin the decoder.
		f.emptyRead++

		if f.emptyRead > maxEmptyReads {
			f.err = io.ErrNoProgress

			return
		}
	}
}

func (f *schemaFilter) classifyEOF(err error) error {
	if !errors.Is(err, io.EOF) {
		return err
	}

	if f.skip.active && f.skip.midToken() {
		return io.ErrUnexpectedEOF
	}

	return io.EOF
}

// consume runs one stream byte through the machine: through the swallow while
// one is active, otherwise through the pass-through scan.
func (f *schemaFilter) consume(b byte) {
	if f.skip.active {
		f.consumeSkip(b)

		return
	}

	f.scan(b)
}

// consumeSkip feeds the swallow one byte and finishes it, if it completed,
// by emitting the replacement value. A byte the value ended on belongs to the
// enclosing stream and is fed back through the scan.
func (f *schemaFilter) consumeSkip(b byte) {
	switch f.skip.step(b) {
	case swallowConsumed:
		// The byte was part of the value; the swallow continues.
	case swallowReprocess:
		// Unreachable: step reruns a reprocess internally.
		f.err = fmt.Errorf("%w: a swallowed byte was left without a state", errSkipMalformedValue)
	case swallowDone, swallowDoneRedo:
		f.skip.active = false
		f.expectKey = false
		f.pending = append(f.pending, '{', '}')

		if f.skip.redoSet {
			f.skip.redoSet = false
			f.scan(f.skip.redo)
		}
	case swallowFailed:
		f.err = f.skip.failure
	default:
		f.err = fmt.Errorf("%w: an unknown swallow result ended the value", errSkipMalformedValue)
	}
}

// scan passes one byte through unchanged and maintains the state that decides
// when a provider_schemas member begins.
func (f *schemaFilter) scan(b byte) {
	switch f.state {
	case stateScan:
		f.pending = append(f.pending, b)
		f.scanTransition(b)
	case stateKey:
		f.scanKey(b)
	case stateColon:
		f.pending = append(f.pending, b)

		switch b {
		case ' ', '\t', '\n', '\r':
			// Whitespace before the separator.
		case ':':
			f.state = stateValue
		default:
			// Not a member separator after all: the decoder will judge the
			// stream, and the scan carries on from this byte.
			f.state = stateScan
			f.scanTransition(b)
		}
	case stateValue:
		f.scanValue(b)
	default:
		// Unreachable: the state set above is closed.
		f.pending = append(f.pending, b)
	}
}

// scanKey compares a key-position string byte against schemaKey. A mismatch
// leaves the match, and the string's own state, to the pass-through scan.
func (f *schemaFilter) scanKey(b byte) {
	f.pending = append(f.pending, b)

	if b == schemaKey[f.keyPos] {
		f.keyPos++

		if f.keyPos == len(schemaKey) {
			f.state = stateColon
		}

		return
	}

	f.state = stateScan
	f.keyPos = -1
	f.keyStringTransition(b)
}

// scanValue delivers whitespace before the value and starts the swallow on the
// value's first byte. A byte that cannot start a JSON value is delivered for
// the decoder to refuse, exactly as the token loop would have.
func (f *schemaFilter) scanValue(b byte) {
	if isSpace(b) {
		f.pending = append(f.pending, b)

		return
	}

	if f.skip.start(b) {
		f.skip.active = true
		f.state = stateScan
		f.expectKey = false

		return
	}

	f.pending = append(f.pending, b)
	f.state = stateScan
	f.scanTransition(b)
}

// scanTransition maintains the structural state used by the gate, and the
// state of a stream byte that appeared outside a swallow.
func (f *schemaFilter) scanTransition(b byte) {
	if f.inString {
		switch {
		case f.escaped:
			f.escaped = false
		case b == '\\':
			f.escaped = true
		case b == '"':
			f.inString = false
		default:
			// A content byte of a string changes nothing.
		}

		return
	}

	switch b {
	case '"':
		if f.expectKey && f.depth == payloadDepth {
			f.state = stateKey
			f.keyPos = 1

			return
		}

		f.inString = true
	case '{', '[':
		f.depth++

		if f.depth == payloadDepth {
			f.objectAtPayloadDepth = b == '{'
			f.expectKey = f.objectAtPayloadDepth
		}
	case '}', ']':
		if f.depth > 0 {
			f.depth--
		}

		f.expectKey = false
	case ',':
		f.expectKey = f.depth == payloadDepth && f.objectAtPayloadDepth
	case ':':
		f.expectKey = false
	default:
		// Any other byte outside a string changes nothing the gate reads.
	}
}

// keyStringTransition restores the string state after a failed key match. The
// matched bytes belong to an ordinary string that is still open, and the
// mismatching byte is its next content byte.
func (f *schemaFilter) keyStringTransition(b byte) {
	f.inString = true

	switch b {
	case '"':
		f.inString = false
		f.escaped = false
	case '\\':
		f.escaped = true
	default:
		f.escaped = false
	}
}

// swallower consumes one JSON value byte-by-byte without holding it, tracking
// enough of the grammar to know where the value ends and to reject input the
// token loop would have rejected.
type swallower struct {
	active bool
	state  swState
	stack  []bool // open containers, true for object and false for array

	keyString bool   // the string in flight is an object key
	escaped   bool   // the previous string byte was a backslash
	unicode   int    // hex digits left in a \uXXXX escape
	literal   string // bytes still expected of true, false or null

	redo    byte // a byte the value ended on, belonging to the enclosing stream
	redoSet bool

	failure error
}

type swState int

const (
	swValue    swState = iota // expecting a value to start
	swObjFirst                // after '{': a key string or '}'
	swObjKey                  // after ',': a key string
	swObjColon                // after a key string: ':'
	swObjNext                 // after a member value: ',' or '}'
	swArrFirst                // after '[': ']' or a value
	swArrNext                 // after an element: ',' or ']'
	swString
	swStringEsc
	swStringUni
	swNumNeg       // after '-'
	swNumInt       // after a nonzero digit
	swNumAfterInt  // integer digits complete: '.', 'e' or the value's end
	swNumFrac      // after '.', digit required
	swNumFracDigit // after fraction digits
	swNumExpSign   // after 'e' or 'E': sign or digit required
	swNumExpFirst  // after a sign: digit required
	swNumExpDigit  // after exponent digits
	swLit          // matching true, false or null
)

type swallowResult int

const (
	swallowConsumed  swallowResult = iota // the byte was part of the value
	swallowReprocess                      // internal: the byte decides the frame's next state; step runs again
	swallowDone                           // the value is complete and the byte was part of it
	swallowDoneRedo                       // the value is complete and the byte belongs outside it
	swallowFailed
)

// maxEmptyReads bounds a source that never returns data or error, so a stuck
// reader surfaces as an error instead of a decoder that never returns.
const maxEmptyReads = 100

// The suffixes of the three JSON literals, matched byte-by-byte after their
// first character has started the match.
const (
	literalTrue  = "true"
	literalFalse = "false"
	literalNull  = "null"
)

// start begins a swallow on the value's first byte, or reports that the byte
// cannot start one.
func (s *swallower) start(b byte) bool {
	switch b {
	case '{':
		s.stack = append(s.stack, true)
		s.state = swObjFirst
	case '[':
		s.stack = append(s.stack, false)
		s.state = swArrFirst
	case '"':
		s.openString(false)

		return true
	case '-':
		s.state = swNumNeg
	case '0':
		s.state = swNumAfterInt
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		s.state = swNumInt
	case literalTrue[0]:
		s.state = swLit
		s.literal = literalTrue[1:]
	case literalFalse[0]:
		s.state = swLit
		s.literal = literalFalse[1:]
	case literalNull[0]:
		s.state = swLit
		s.literal = literalNull[1:]
	default:
		return false
	}

	return true
}

// step advances the swallow by one byte. States that hand a byte to a new
// context return swallowReprocess, which reruns the dispatch on the same byte,
// so a result that returns to the caller is final.
func (s *swallower) step(b byte) swallowResult {
	for {
		if res := s.dispatch(b); res != swallowReprocess {
			return res
		}
	}
}

// dispatch advances the state machine by one byte in its current state.
func (s *swallower) dispatch(b byte) swallowResult {
	switch s.state {
	case swValue:
		return s.stepValue(b)
	case swObjFirst:
		return s.member(b, '}', true)
	case swObjKey:
		return s.stepObjectKey(b)
	case swObjColon:
		return s.stepObjectColon(b)
	case swObjNext:
		return s.member(b, '}', false)
	case swArrFirst:
		return s.stepArrayFirst(b)
	case swArrNext:
		return s.stepArrayNext(b)
	case swString:
		return s.stepString(b)
	case swStringEsc:
		return s.stepStringEscape(b)
	case swStringUni:
		return s.stepStringUnicode(b)
	case swNumNeg:
		return s.stepNumberNeg(b)
	case swNumInt:
		return s.stepNumberInt(b)
	case swNumAfterInt:
		return s.stepNumberIntDone(b)
	case swNumFrac:
		return s.stepNumberFrac(b)
	case swNumFracDigit:
		return s.stepNumberFracDigit(b)
	case swNumExpSign:
		return s.stepNumberExpSign(b)
	case swNumExpFirst:
		return s.stepNumberExpFirst(b)
	case swNumExpDigit:
		return s.stepNumberExpDigit(b)
	case swLit:
		return s.stepLiteral(b)
	default:
		s.failure = fmt.Errorf("%w: an unknown state held the value open", errSkipMalformedValue)

		return swallowFailed
	}
}

// stepValue accepts whitespace while the swallow waits for a value: the first
// one after a member or element separator.
func (s *swallower) stepValue(b byte) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	if !s.start(b) {
		return s.fail(b, "where a value belongs")
	}

	return swallowConsumed
}

// member handles the two object states around a member: the first member
// after '{' accepts a key string or the closer, and swObjNext after a member
// value accepts a separator or the closer — a bare key string there is the
// invalid input the token loop's scanner refuses.
func (s *swallower) member(b, closer byte, first bool) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	if first {
		switch b {
		case '"':
			return s.openString(true)
		case closer:
			return s.pop()
		default:
			return s.fail(b, "where an object key or closer belongs")
		}
	}

	switch b {
	case ',':
		s.state = swObjKey

		return swallowConsumed
	case closer:
		return s.pop()
	default:
		return s.fail(b, "where an object separator belongs")
	}
}

func (s *swallower) stepObjectKey(b byte) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	if b != '"' {
		return s.fail(b, "where an object key belongs")
	}

	return s.openString(true)
}

func (s *swallower) stepObjectColon(b byte) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	if b != ':' {
		return s.fail(b, "where an object separator belongs")
	}

	s.state = swValue

	return swallowConsumed
}

func (s *swallower) stepArrayFirst(b byte) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	if b == ']' {
		return s.pop()
	}

	s.state = swValue

	return swallowReprocess
}

func (s *swallower) stepArrayNext(b byte) swallowResult {
	if isSpace(b) {
		return swallowConsumed
	}

	switch b {
	case ',':
		s.state = swValue
	case ']':
		return s.pop()
	default:
		return s.fail(b, "where an array separator belongs")
	}

	return swallowConsumed
}

func (s *swallower) stepString(b byte) swallowResult {
	switch {
	case b < firstControlByte:
		return s.fail(b, "inside a string")
	case b == '"':
		return s.closeString()
	case b == '\\':
		s.state = swStringEsc
	default:
		// A content byte of a string changes nothing.
	}

	return swallowConsumed
}

func (s *swallower) stepStringEscape(b byte) swallowResult {
	switch b {
	case 'u':
		s.state = swStringUni
		s.unicode = unicodeDigits
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		s.state = swString
	default:
		return s.fail(b, "in an escape")
	}

	return swallowConsumed
}

func (s *swallower) stepStringUnicode(b byte) swallowResult {
	if !isHexDigit(b) {
		return s.fail(b, "in a unicode escape")
	}

	s.unicode--

	if s.unicode == 0 {
		s.state = swString
	}

	return swallowConsumed
}

func (s *swallower) stepNumberNeg(b byte) swallowResult {
	switch {
	case b == '0':
		s.state = swNumAfterInt
	case isDigit(b):
		s.state = swNumInt
	default:
		return s.fail(b, "where a number belongs")
	}

	return swallowConsumed
}

func (s *swallower) stepNumberInt(b byte) swallowResult {
	if isDigit(b) {
		return swallowConsumed
	}

	s.state = swNumAfterInt

	return swallowReprocess
}

// stepNumberIntDone ends an integer on a byte that is not part of it.
func (s *swallower) stepNumberIntDone(b byte) swallowResult {
	switch b {
	case '.':
		s.state = swNumFrac
	case 'e', 'E':
		s.state = swNumExpSign
	default:
		return s.finishScalar(b)
	}

	return swallowConsumed
}

func (s *swallower) stepNumberFrac(b byte) swallowResult {
	if !isDigit(b) {
		return s.fail(b, "where a fraction digit belongs")
	}

	s.state = swNumFracDigit

	return swallowConsumed
}

func (s *swallower) stepNumberFracDigit(b byte) swallowResult {
	switch {
	case isDigit(b):
		return swallowConsumed
	case b == 'e' || b == 'E':
		s.state = swNumExpSign

		return swallowConsumed
	default:
		return s.finishScalar(b)
	}
}

func (s *swallower) stepNumberExpSign(b byte) swallowResult {
	switch {
	case b == '+' || b == '-':
		s.state = swNumExpFirst
	case isDigit(b):
		s.state = swNumExpDigit
	default:
		return s.fail(b, "where an exponent belongs")
	}

	return swallowConsumed
}

func (s *swallower) stepNumberExpFirst(b byte) swallowResult {
	if !isDigit(b) {
		return s.fail(b, "where an exponent digit belongs")
	}

	s.state = swNumExpDigit

	return swallowConsumed
}

func (s *swallower) stepNumberExpDigit(b byte) swallowResult {
	if isDigit(b) {
		return swallowConsumed
	}

	return s.finishScalar(b)
}

func (s *swallower) stepLiteral(b byte) swallowResult {
	if b != s.literal[0] {
		return s.fail(b, "in a literal")
	}

	s.literal = s.literal[1:]

	if s.literal == "" {
		return s.closeScalar()
	}

	return swallowConsumed
}

// openString enters the string state, recording whether the string is an
// object key, which is followed by a separator rather than a completed value.
func (s *swallower) openString(key bool) swallowResult {
	s.state = swString
	s.keyString = key
	s.escaped = false

	return swallowConsumed
}

// closeString ends a string: as an object key it is followed by a separator,
// inside a container it is a completed value, and at the top of the swallow it
// is the whole value.
func (s *swallower) closeString() swallowResult {
	if s.keyString {
		s.state = swObjColon

		return swallowConsumed
	}

	if len(s.stack) > 0 {
		s.state = s.frameNext()

		return swallowConsumed
	}

	return swallowDone
}

// closeScalar completes a literal, whose last byte was part of it.
func (s *swallower) closeScalar() swallowResult {
	if len(s.stack) > 0 {
		s.state = s.frameNext()

		return swallowConsumed
	}

	return swallowDone
}

// finishScalar completes a number, which ends on a byte that is not part of
// it. Inside a container that byte decides the container's next state and the
// step runs it again there; at the top of the swallow it belongs to the
// enclosing stream and is pushed back to the filter.
func (s *swallower) finishScalar(b byte) swallowResult {
	if len(s.stack) > 0 {
		s.state = s.frameNext()

		return swallowReprocess
	}

	s.redo = b
	s.redoSet = true

	return swallowDoneRedo
}

// pop closes a container. Closing the outermost one completes the swallow.
func (s *swallower) pop() swallowResult {
	s.stack = s.stack[:len(s.stack)-1]

	if len(s.stack) == 0 {
		return swallowDone
	}

	s.state = s.frameNext()

	return swallowConsumed
}

// frameNext names the state that follows a completed value inside the
// innermost open container.
func (s *swallower) frameNext() swState {
	if s.stack[len(s.stack)-1] {
		return swObjNext
	}

	return swArrNext
}

func (s *swallower) fail(b byte, where string) swallowResult {
	s.failure = fmt.Errorf("%w: %q %s", errSkipMalformedValue, b, where)

	return swallowFailed
}

// midTokenStates marks the swState values where an end of input falls inside
// a token, where the token loop would have reported an unexpected end rather
// than a clean one. Between tokens the loop reads the end of input as the end
// of the stream.
//
//nolint:gochecknoglobals // an immutable lookup table.
var midTokenStates = [...]bool{
	swString:      true,
	swStringEsc:   true,
	swStringUni:   true,
	swNumNeg:      true,
	swNumFrac:     true,
	swNumExpSign:  true,
	swNumExpFirst: true,
	swLit:         true,
}

// midToken reports whether an end of input inside the swallow falls inside a
// token.
func (s *swallower) midToken() bool {
	return midTokenStates[s.state]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isHexDigit(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
