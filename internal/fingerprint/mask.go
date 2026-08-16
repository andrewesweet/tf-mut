package fingerprint

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Span is what survives masking at one path: the components observed to be
// stable, with the volatile middle replaced by a marker.
//
// Masking is component-granular by disposition R2-9 — `"${uuid()}-stable"` has
// an assertable stable suffix, and masking the whole value would erase a real
// finding — so a mask records the retained prefix and suffix rather than a
// simple "ignore this path".
type Span struct {
	// Prefix is the leading run of characters observed to be stable.
	Prefix string
	// Suffix is the trailing run of characters observed to be stable.
	Suffix string
	// Whole marks a value that is volatile in its entirety, which is a sound
	// decomposition with no stable component rather than an undecidable one.
	Whole bool
	// FromSyntax marks a span the static scan derived from the module's syntax
	// rather than from observing two runs.
	//
	// The distinction decides which of two spans survives a merge, and the
	// R2-9 disposition is the reason: component granularity is what the AST
	// shows about a template, and comparing two observations of a value can
	// only say that the whole of it moved. A run-derived span would have to
	// guess which shared characters were stable and which coincided, and a
	// wrong guess is a mutant classified differently on different days.
	FromSyntax bool
}

// Mask is the volatile set: what to remove from a payload before comparing it.
type Mask struct {
	// Spans maps canonical path to the components that survive.
	Spans map[string]Span
	// Undecidable lists paths whose volatility could not be decomposed. A
	// payload touching one is `indeterminate` and is never treated as identical.
	Undecidable map[string]bool
}

// NewMask returns an empty mask.
func NewMask() Mask {
	return Mask{Spans: map[string]Span{}, Undecidable: map[string]bool{}}
}

// SyntaxSpan builds a span the static scan derived from a module's syntax.
func SyntaxSpan(prefix, suffix string, whole bool) Span {
	return Span{Prefix: prefix, Suffix: suffix, Whole: whole, FromSyntax: true}
}

// Empty reports whether the mask removes nothing.
func (m Mask) Empty() bool {
	return len(m.Spans) == 0 && len(m.Undecidable) == 0
}

// Paths lists the masked paths, for the report's mask evidence.
func (m Mask) Paths() []string {
	paths := make([]string, 0, len(m.Spans)+len(m.Undecidable))
	for path := range m.Spans {
		paths = append(paths, path)
	}

	for path := range m.Undecidable {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	return paths
}

// Merge returns the union of two masks, the more conservative entry winning.
func (m Mask) Merge(other Mask) Mask {
	return m.union(other, false)
}

// MergeMutantVolatility folds a mask derived from two runs of the *mutant* into
// the baseline mask.
//
// A path the baseline already knew was volatile keeps its span: the mutant only
// confirmed it. A path that was stable in the baseline and moves under the
// mutant is different in kind — masking it on both sides would erase the very
// difference the mutation introduced and let the oracle claim an equality
// nobody can stand behind. Such a path is undecidable, which is what the C4
// rule calls residual undecidability.
func (m Mask) MergeMutantVolatility(other Mask) Mask {
	return m.union(other, true)
}

// narrower resolves two spans for the same path.
//
// A span the syntax produced wins over one two runs produced: the syntax knows
// which subcomponent is impure, and the observation only knows that the value
// moved. Between spans of the same provenance the smaller stable claim wins,
// because a component one source calls volatile must not be reinstated by
// another calling it stable.
func narrower(left, right Span) Span {
	if left.FromSyntax != right.FromSyntax {
		if left.FromSyntax {
			return left
		}

		return right
	}

	if left.Whole || right.Whole {
		return Span{Prefix: "", Suffix: "", Whole: true, FromSyntax: left.FromSyntax}
	}

	return Span{
		Prefix:     commonPrefix(left.Prefix, right.Prefix),
		Suffix:     commonSuffix(left.Suffix, right.Suffix),
		Whole:      false,
		FromSyntax: left.FromSyntax,
	}
}

// Derive computes the volatile set from two runs of the same configuration.
//
// A value that moved between two otherwise identical runs is volatile; what
// stayed put on either end of it is not, and stays in the fingerprint.
func Derive(first, second []Payload) Mask {
	mask := NewMask()

	byKey := map[string]Payload{}
	for _, payload := range second {
		byKey[payload.Key()] = payload
	}

	for _, payload := range first {
		other, found := byKey[payload.Key()]
		if !found {
			// A run block one baseline run fingerprinted and the other did not
			// is a shape difference across the whole run: nothing about it can
			// be decomposed, and letting it vanish here would let a later
			// comparison call the run identical.
			for path := range payload.Values {
				mask.Undecidable[path] = true
			}

			continue
		}

		derivePayload(payload, other, mask)
	}

	return mask
}

func derivePayload(first, second Payload, mask Mask) {
	for path, value := range first.Values {
		otherValue, found := second.Values[path]
		if !found {
			// A path present in one run and absent from the other is a shape
			// difference, not a value difference: nothing can be decomposed.
			mask.Undecidable[path] = true

			continue
		}

		if value == otherValue {
			continue
		}

		span, ok := decompose(value, otherValue)
		if !ok {
			mask.Undecidable[path] = true

			continue
		}

		mask.Spans[path] = span
	}

	for path := range second.Values {
		if _, found := first.Values[path]; !found {
			mask.Undecidable[path] = true
		}
	}
}

// decompose separates the stable components of two renderings of one path.
//
// Two observations of a value can establish that the value moved and nothing
// finer. Two random identifiers share a leading character about six times in a
// hundred, so a span inferred from the characters they happen to have in common
// would call a volatile character stable, and every later run whose value
// disagreed would be undecidable — a verdict that changed with the weather.
// A value observed to move is therefore masked whole, and component
// granularity comes from the syntax instead (R2-9), where the impure
// subcomponent is visible rather than inferred.
//
// Two values of different kinds are not a moving value at all: they are a shape
// change, and nothing about them can be decomposed.
func decompose(left, right string) (Span, bool) {
	_, leftIsString := unquote(left)
	_, rightIsString := unquote(right)

	if leftIsString != rightIsString || isContainer(left) != isContainer(right) {
		return Span{Prefix: "", Suffix: "", Whole: false, FromSyntax: false}, false
	}

	return Span{Prefix: "", Suffix: "", Whole: true, FromSyntax: false}, true
}

func isContainer(rendering string) bool {
	return rendering == emptyObject || rendering == emptyArray
}

// Apply removes the volatile components from a payload.
//
// The second return reports that the payload touched an undecidable path, which
// makes the whole fingerprint `indeterminate`: never equal to anything, so no
// mutant is ever excluded on the strength of volatility nobody could pin down.
func (m Mask) Apply(payload Payload) (Payload, bool) {
	if m.Empty() {
		return payload, false
	}

	masked := make(map[string]string, len(payload.Values))
	indeterminate := false

	for path, value := range payload.Values {
		if m.Undecidable[path] {
			indeterminate = true

			continue
		}

		span, found := m.Spans[path]
		if !found {
			masked[path] = value

			continue
		}

		replaced, ok := span.apply(value)
		if !ok {
			indeterminate = true

			continue
		}

		masked[path] = replaced
	}

	result := payload
	result.Values = masked

	return result, indeterminate
}

// apply replaces the volatile middle of a value, keeping the stable ends.
//
// A value too short to carry both stable ends, or one that no longer starts
// with the stable prefix, cannot be decomposed against this span: reporting
// that honestly is what keeps the fingerprint from claiming a false identity.
func (s Span) apply(value string) (string, bool) {
	if s.Whole {
		return volatileMarker, true
	}

	text, isString := unquote(value)
	if !isString {
		return volatileMarker, true
	}

	if len(text) < len(s.Prefix)+len(s.Suffix) {
		return "", false
	}

	if !strings.HasPrefix(text, s.Prefix) {
		return "", false
	}

	return strconv.Quote(s.Prefix + volatileMarker + text[len(text)-len(s.Suffix):]), true
}

// unquote returns the text of a canonical string rendering.
func unquote(rendering string) (string, bool) {
	if len(rendering) < 2 || rendering[0] != '"' {
		return "", false
	}

	text, err := strconv.Unquote(rendering)
	if err != nil {
		return "", false
	}

	return text, true
}

func commonPrefix(left, right string) string {
	length := 0
	for length < len(left) && length < len(right) && left[length] == right[length] {
		length++
	}

	return left[:length]
}

func commonSuffix(left, right string) string {
	length := 0
	for length < len(left) && length < len(right) &&
		left[len(left)-1-length] == right[len(right)-1-length] {
		length++
	}

	return left[len(left)-length:]
}

// Undecidables lists the paths whose volatility the mask could not decompose,
// which is the evidence an indeterminate comparison carries.
func (m Mask) Undecidables() []string {
	paths := make([]string, 0, len(m.Undecidable))
	for path := range m.Undecidable {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	return paths
}

// union combines two masks. When newIsUndecidable is set, a span the receiver
// does not already carry becomes undecidable rather than being adopted.
func (m Mask) union(other Mask, newIsUndecidable bool) Mask {
	merged := NewMask()
	maps.Copy(merged.Spans, m.Spans)
	maps.Copy(merged.Undecidable, m.Undecidable)

	for path, span := range other.Spans {
		existing, known := m.Spans[path]

		switch {
		case known:
			merged.Spans[path] = narrower(existing, span)
		case newIsUndecidable:
			merged.Undecidable[path] = true
		default:
			merged.Spans[path] = span
		}
	}

	for path := range other.Undecidable {
		merged.Undecidable[path] = true
	}

	for path := range merged.Undecidable {
		delete(merged.Spans, path)
	}

	return merged
}
