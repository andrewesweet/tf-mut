package config

import (
	"slices"
	"strings"
)

// Directive is one inline suppression comment.
//
// A directive without a reason **does not suppress**. The finding stands and
// the directive is reported as rejected, because "a reason is mandatory" and
// "a missing reason is a warning" cannot both be true, and the first is the one
// worth having: a suppression whose justification nobody wrote is a finding
// somebody hid.
type Directive struct {
	// File is the closure-relative path of the file carrying the comment.
	File string
	// Line is the line the comment sits on. The directive applies to sites
	// beginning on the following line.
	Line int
	// Column is where the comment starts.
	Column int
	// Operators are the identifiers the directive names.
	Operators []string
	// Reason is the mandatory justification.
	Reason string
	// Rejection explains why the directive suppresses nothing, when it does not.
	Rejection string
}

// Valid reports whether the directive suppresses anything at all.
func (d Directive) Valid() bool {
	return d.Rejection == ""
}

// Applies reports whether the directive covers a mutant of this operator
// beginning on this line.
func (d Directive) Applies(operator string, line int) bool {
	if line != d.Line+1 {
		return false
	}

	return slices.Contains(d.Operators, operator)
}

// The directive's syntax.
const (
	marker      = "tf-mut:disable"
	emDash      = "—"
	doubleDash  = "--"
	commentHash = "#"
	commentSlim = "//"
)

// The rejection messages, which the report carries verbatim.
const (
	rejectNoOperators = "the directive names no operator, so there is nothing to suppress"
	rejectNoReason    = "the directive gives no reason, so it does not suppress: " +
		"write `# tf-mut:disable <operator-ids> " + emDash + " <why>`"
)

// Directives extracts every suppression comment from a source file.
func Directives(file string, source []byte) []Directive {
	directives := []Directive{}

	for index, line := range strings.Split(string(source), "\n") {
		comment, column, found := commentOn(line)
		if !found {
			continue
		}

		body, marked := strings.CutPrefix(comment, marker)
		if !marked {
			continue
		}

		directives = append(directives, parseDirective(file, index+1, column, body))
	}

	return directives
}

// commentOn returns the comment text of a line, if it has one.
func commentOn(line string) (string, int, bool) {
	for _, opener := range []string{commentHash, commentSlim} {
		if offset := strings.Index(line, opener); offset >= 0 {
			return strings.TrimSpace(line[offset+len(opener):]), offset + 1, true
		}
	}

	return "", 0, false
}

func parseDirective(file string, line, column int, body string) Directive {
	names, reason := split(body)

	directive := Directive{
		File:      file,
		Line:      line,
		Column:    column,
		Operators: operators(names),
		Reason:    strings.TrimSpace(reason),
		Rejection: "",
	}

	switch {
	case len(directive.Operators) == 0:
		directive.Rejection = rejectNoOperators
	case directive.Reason == "":
		directive.Rejection = rejectNoReason
	default:
	}

	return directive
}

// split separates the operator list from the reason.
func split(body string) (names, reason string) {
	for _, separator := range []string{emDash, doubleDash, ":"} {
		if before, after, found := strings.Cut(body, separator); found {
			return before, after
		}
	}

	return body, ""
}

func operators(names string) []string {
	found := []string{}

	for name := range strings.FieldsSeq(strings.ReplaceAll(names, ",", " ")) {
		found = append(found, strings.TrimSpace(name))
	}

	return found
}
