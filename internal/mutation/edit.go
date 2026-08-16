package mutation

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// part is one contiguous byte replacement inside a source file.
type part struct {
	rng  hcl.Range
	text string
}

// edit is one mutant expressed as byte replacements against the original file.
//
// Expression operators rewrite byte ranges rather than re-printing the block
// through hclwrite: a token-range replacement leaves every byte outside the
// operator's own range untouched, which is what keeps a mutant diff to the
// lines the operator owns even in a heredoc or a multi-line collection.
type edit struct {
	operator Operator
	// parts are disjoint and may be given in any order.
	parts []part
	// site is the semantic address the mutant reports.
	site string
	// resource is the managed resource address the site belongs to.
	resource string
}

// span is the source range the edit covers, which is what the report shows.
func (e edit) span() hcl.Range {
	span := e.parts[0].rng
	for _, current := range e.parts[1:] {
		span = hcl.RangeBetween(span, current.rng)
	}

	return span
}

// apply splices the replacements into the source, last first so that earlier
// offsets stay valid.
func (e edit) apply(source []byte) []byte {
	ordered := make([]part, len(e.parts))
	copy(ordered, e.parts)

	slices.SortFunc(ordered, func(left, right part) int {
		return right.rng.Start.Byte - left.rng.Start.Byte
	})

	result := source

	for _, current := range ordered {
		if current.rng.Start.Byte < 0 || current.rng.End.Byte > len(result) ||
			current.rng.Start.Byte > current.rng.End.Byte {
			return nil
		}

		spliced := make([]byte, 0, len(result)-(current.rng.End.Byte-current.rng.Start.Byte)+len(current.text))
		spliced = append(spliced, result[:current.rng.Start.Byte]...)
		spliced = append(spliced, current.text...)
		spliced = append(spliced, result[current.rng.End.Byte:]...)
		result = spliced
	}

	return result
}

// replacement is the text of a single-part edit, used to derive identifiers.
func (e edit) replacement() string {
	texts := make([]string, 0, len(e.parts))
	for _, current := range e.parts {
		texts = append(texts, current.text)
	}

	return strings.Join(texts, "\x1f")
}

// original is the source text the edit replaces, used to derive identifiers.
func (e edit) original(source []byte) string {
	texts := make([]string, 0, len(e.parts))
	for _, current := range e.parts {
		texts = append(texts, sourceText(source, current.rng))
	}

	return strings.Join(texts, "\x1f")
}

// sourceText returns the exact bytes a range covers.
func sourceText(source []byte, rng hcl.Range) string {
	if rng.Start.Byte < 0 || rng.End.Byte > len(source) || rng.Start.Byte > rng.End.Byte {
		return ""
	}

	return string(source[rng.Start.Byte:rng.End.Byte])
}

// replace builds a single-part edit.
func replace(operator Operator, where site, rng hcl.Range, text string) edit {
	return edit{
		operator: operator,
		parts:    []part{{rng: rng, text: text}},
		site:     where.address,
		resource: where.resource,
	}
}

// remove builds an edit that deletes a range.
func remove(operator Operator, where site, rng hcl.Range) edit {
	return replace(operator, where, rng, "")
}

// lineRange expands a range to the whole lines it occupies, including the
// trailing newline, so that deleting a construct does not leave a blank line
// behind and turn a one-line diff into a two-line one.
func lineRange(source []byte, rng hcl.Range) hcl.Range {
	start := rng.Start.Byte
	for start > 0 && source[start-1] != '\n' {
		if source[start-1] != ' ' && source[start-1] != '\t' {
			return rng
		}

		start--
	}

	end := rng.End.Byte
	for end < len(source) && source[end] != '\n' {
		if source[end] != ' ' && source[end] != '\t' {
			return rng
		}

		end++
	}

	if end < len(source) {
		end++
	}

	expanded := rng
	expanded.Start.Byte = start
	expanded.End.Byte = end

	return expanded
}

// between is the range of the source lying between two ranges, which is where
// an operator token or keyword sits.
func between(left, right hcl.Range) hcl.Range {
	span := left
	span.Start = left.End
	span.End = right.Start

	return span
}

// findToken locates a token inside a range and returns the range it occupies.
func findToken(source []byte, within hcl.Range, token string) (hcl.Range, bool) {
	text := sourceText(source, within)

	offset := strings.Index(text, token)
	if offset < 0 {
		return hcl.Range{}, false //nolint:exhaustruct // the caller ignores it.
	}

	found := within
	found.Start.Byte = within.Start.Byte + offset
	found.End.Byte = found.Start.Byte + len(token)

	return found, true
}
