package mutation

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// expressionEdits offers every Tier 1 operator the expression in front of it.
//
// Operators fire on the node, not on text, and each one is responsible for its
// own applicability: the matrix in docs/design/mutation-operators.md is the
// specification these functions implement, row for row.
func expressionEdits(source []byte, where site, expr hclsyntax.Expression) []edit {
	switch typed := expr.(type) {
	case *hclsyntax.ConditionalExpr:
		return conditionalEdits(source, where, typed)
	case *hclsyntax.BinaryOpExpr:
		return binaryEdits(source, where, typed)
	case *hclsyntax.UnaryOpExpr:
		return unaryEdits(source, where, typed)
	case *hclsyntax.LiteralValueExpr:
		return literalEdits(where, typed)
	case *hclsyntax.TemplateExpr:
		return templateEdits(source, where, typed)
	case *hclsyntax.TupleConsExpr:
		return tupleEdits(source, where, typed)
	case *hclsyntax.ObjectConsExpr:
		return objectEdits(source, where, typed)
	case *hclsyntax.ForExpr:
		return forEdits(source, where, typed)
	case *hclsyntax.IndexExpr:
		return indexEdits(where, typed)
	case *hclsyntax.ScopeTraversalExpr:
		return traversalEdits(where, typed)
	case *hclsyntax.SplatExpr:
		return splatEdits(source, where, typed)
	case *hclsyntax.FunctionCallExpr:
		return functionEdits(source, where, typed)
	default:
		return nil
	}
}

// isTemplateConditional reports a `%{if}` inside a template rather than the
// ternary form. The two share an AST node but not a syntax, so the ternary
// rewrites would produce text that does not parse.
func isTemplateConditional(source []byte, expr *hclsyntax.ConditionalExpr) bool {
	return strings.HasPrefix(sourceText(source, expr.SrcRange), "%{")
}

func conditionalEdits(source []byte, where site, expr *hclsyntax.ConditionalExpr) []edit {
	trueText := sourceText(source, expr.TrueResult.Range())
	falseText := sourceText(source, expr.FalseResult.Range())

	if isTemplateConditional(source, expr) {
		return []edit{
			replace(TplIfCollapse, where, expr.SrcRange, trueText),
			replace(TplIfCollapse, where, expr.SrcRange, falseText),
		}
	}

	condition := sourceText(source, expr.Condition.Range())

	return []edit{
		{
			operator: CondSwap,
			parts: []part{
				{rng: expr.TrueResult.Range(), text: falseText},
				{rng: expr.FalseResult.Range(), text: trueText},
			},
			site: where.address, resource: where.resource,
		},
		replace(CondNegate, where, expr.Condition.Range(), "!("+condition+")"),
		replace(CondTrue, where, expr.SrcRange, trueText),
		replace(CondFalse, where, expr.SrcRange, falseText),
	}
}

// binaryReplacement names the operator token of a binary operation and the
// substitutions each catalogue entry makes for it.
type binaryReplacement struct {
	token        string
	substitution map[Operator]string
}

//nolint:gochecknoglobals // an immutable lookup table.
var binaryOperations = map[*hclsyntax.Operation]binaryReplacement{
	hclsyntax.OpLogicalAnd: {"&&", map[Operator]string{BoolAndOr: "||"}},
	hclsyntax.OpLogicalOr:  {"||", map[Operator]string{BoolOrAnd: "&&"}},
	hclsyntax.OpEqual:      {"==", map[Operator]string{CmpEqNe: "!="}},
	hclsyntax.OpNotEqual:   {"!=", map[Operator]string{CmpEqNe: "=="}},
	hclsyntax.OpLessThan: {"<", map[Operator]string{
		CmpBoundary: "<=", CmpInvert: ">",
	}},
	hclsyntax.OpLessThanOrEqual: {"<=", map[Operator]string{
		CmpBoundary: "<", CmpInvert: ">=",
	}},
	hclsyntax.OpGreaterThan: {">", map[Operator]string{
		CmpBoundary: ">=", CmpInvert: "<",
	}},
	hclsyntax.OpGreaterThanOrEqual: {">=", map[Operator]string{
		CmpBoundary: ">", CmpInvert: "<=",
	}},
	hclsyntax.OpAdd:      {"+", map[Operator]string{ArithSwap: "-"}},
	hclsyntax.OpSubtract: {"-", map[Operator]string{ArithSwap: "+"}},
	hclsyntax.OpMultiply: {"*", map[Operator]string{ArithSwap: "/"}},
	hclsyntax.OpDivide:   {"/", map[Operator]string{ArithSwap: "*"}},
}

// booleanOperations produce a boolean, which is the type evidence
// BOOL-NEGATE-INSERT needs in order to stay type-preserving.
//
//nolint:gochecknoglobals // an immutable lookup table.
var booleanOperations = map[*hclsyntax.Operation]bool{
	hclsyntax.OpLogicalAnd:         true,
	hclsyntax.OpLogicalOr:          true,
	hclsyntax.OpEqual:              true,
	hclsyntax.OpNotEqual:           true,
	hclsyntax.OpLessThan:           true,
	hclsyntax.OpLessThanOrEqual:    true,
	hclsyntax.OpGreaterThan:        true,
	hclsyntax.OpGreaterThanOrEqual: true,
}

func binaryEdits(source []byte, where site, expr *hclsyntax.BinaryOpExpr) []edit {
	edits := []edit{}

	operation, known := binaryOperations[expr.Op]
	if !known {
		return edits
	}

	gap := between(expr.LHS.Range(), expr.RHS.Range())

	token, found := findToken(source, gap, operation.token)
	if !found {
		return edits
	}

	for _, operator := range sortedOperators(operation.substitution) {
		edits = append(edits, replace(operator, where, token, operation.substitution[operator]))
	}

	if booleanOperations[expr.Op] {
		edits = append(edits, replace(BoolNegateInsert, where, expr.SrcRange,
			"!("+sourceText(source, expr.SrcRange)+")"))
	}

	return edits
}

func sortedOperators(substitutions map[Operator]string) []Operator {
	operators := make([]Operator, 0, len(substitutions))
	for operator := range substitutions {
		operators = append(operators, operator)
	}

	for index := 1; index < len(operators); index++ {
		for back := index; back > 0 && operators[back] < operators[back-1]; back-- {
			operators[back], operators[back-1] = operators[back-1], operators[back]
		}
	}

	return operators
}

func unaryEdits(source []byte, where site, expr *hclsyntax.UnaryOpExpr) []edit {
	if expr.Op != hclsyntax.OpLogicalNot {
		return nil
	}

	return []edit{
		replace(BoolNegateRemove, where, expr.SrcRange, sourceText(source, expr.Val.Range())),
	}
}

func literalEdits(where site, expr *hclsyntax.LiteralValueExpr) []edit {
	value := expr.Val

	if value.Type() == cty.Bool && !value.IsNull() {
		return []edit{
			replace(BoolLiteralFlip, where, expr.SrcRange, strconv.FormatBool(!value.True())),
		}
	}

	if value.Type() != cty.Number || value.IsNull() {
		return nil
	}

	number := value.AsBigFloat()
	edits := []edit{
		replace(NumOffByOne, where, expr.SrcRange, shifted(number, 1)),
		replace(NumOffByOne, where, expr.SrcRange, shifted(number, -1)),
	}

	if number.Sign() != 0 {
		edits = append(edits,
			replace(NumZero, where, expr.SrcRange, "0"),
			replace(NumNegate, where, expr.SrcRange, shiftedNegation(number)))
	}

	return edits
}

func shifted(number *big.Float, delta int64) string {
	result := new(big.Float).Add(number, big.NewFloat(float64(delta)))

	return formatNumber(result)
}

func shiftedNegation(number *big.Float) string {
	return formatNumber(new(big.Float).Neg(number))
}

func formatNumber(number *big.Float) string {
	if number.IsInt() {
		integer, _ := number.Int(nil)

		return integer.String()
	}

	return number.Text('f', -1)
}

// isStringLiteral reports a quoted template with no interpolation, which is the
// only form the string operators may rewrite: a heredoc's bytes carry
// indentation meaning that requoting would destroy.
func isStringLiteral(source []byte, expr *hclsyntax.TemplateExpr) (string, bool) {
	text := sourceText(source, expr.SrcRange)
	if !strings.HasPrefix(text, `"`) || !expr.IsStringLiteral() {
		return "", false
	}

	unquoted, err := strconv.Unquote(text)
	if err != nil {
		return "", false
	}

	return unquoted, true
}

func templateEdits(source []byte, where site, expr *hclsyntax.TemplateExpr) []edit {
	if literal, ok := isStringLiteral(source, expr); ok {
		return stringLiteralEdits(where, expr, literal)
	}

	edits := []edit{}
	text := sourceText(source, expr.SrcRange)

	if strings.HasPrefix(text, "<<-") {
		marker := expr.SrcRange
		marker.End.Byte = marker.Start.Byte + len("<<-")
		edits = append(edits, replace(HeredocIndentFlip, where, marker, "<<"))
	}

	edits = append(edits, literalSegmentEdits(source, where, expr)...)

	return append(edits, interpolationEdits(source, where, expr)...)
}

// literalSegmentEdits fires the string operators on the literal segments of an
// interpolated template.
//
// A segment is a string literal in every sense that matters — `"${uuid()}-stable"`
// has an assertable `-stable` in it, and that is precisely the component the
// volatility work exists to keep in the fingerprint. Segments whose source
// carries an escape are skipped: case-flipping `\n` produces `\N`, which is not
// a mutation of the module but a defect in the operator.
func literalSegmentEdits(source []byte, where site, expr *hclsyntax.TemplateExpr) []edit {
	if len(expr.Parts) < minimumElements {
		return nil
	}

	edits := []edit{}

	for _, current := range expr.Parts {
		literal, ok := current.(*hclsyntax.LiteralValueExpr)
		if !ok || literal.Val.Type() != cty.String || literal.Val.IsNull() {
			continue
		}

		text := sourceText(source, literal.SrcRange)
		if text == "" || strings.Contains(text, `\`) {
			continue
		}

		edits = append(edits, remove(StrEmpty, where, literal.SrcRange))

		if flipped := flipCase(text); flipped != text {
			edits = append(edits, replace(StrCase, where, literal.SrcRange, flipped))
		}
	}

	return edits
}

func stringLiteralEdits(where site, expr *hclsyntax.TemplateExpr, literal string) []edit {
	edits := []edit{}

	if literal != "" {
		edits = append(edits, replace(StrEmpty, where, expr.SrcRange, `""`))
	}

	if flipped := flipCase(literal); flipped != literal {
		edits = append(edits, replace(StrCase, where, expr.SrcRange, strconv.Quote(flipped)))
	}

	return edits
}

func flipCase(text string) string {
	flipped := strings.Map(func(letter rune) rune {
		switch {
		case letter >= 'a' && letter <= 'z':
			return letter - 'a' + 'A'
		case letter >= 'A' && letter <= 'Z':
			return letter - 'A' + 'a'
		default:
			return letter
		}
	}, text)

	return flipped
}

// interpolationEdits drops one interpolation at a time and flips the strip
// markers that are present.
//
// A strip marker is never *added*: `${e}` and `${~e}` differ only where
// adjacent whitespace exists, so adding one would emit mutants that are
// unobservable by construction and say nothing about the suite.
func interpolationEdits(source []byte, where site, expr *hclsyntax.TemplateExpr) []edit {
	edits := []edit{}

	for _, current := range expr.Parts {
		if _, literal := current.(*hclsyntax.LiteralValueExpr); literal {
			continue
		}

		span, found := interpolationRange(source, expr.SrcRange, current.Range())
		if !found {
			continue
		}

		edits = append(edits, remove(TplDropInterp, where, span))
		edits = append(edits, stripMarkerEdits(source, where, span)...)
	}

	return edits
}

// interpolationRange widens an interpolated expression's range to the `${` and
// `}` that delimit it.
func interpolationRange(source []byte, template, inner hcl.Range) (hcl.Range, bool) {
	start := inner.Start.Byte
	for start > template.Start.Byte && !strings.HasPrefix(string(source[start:]), "${") {
		start--
	}

	if !strings.HasPrefix(string(source[start:]), "${") {
		return hcl.Range{}, false //nolint:exhaustruct // the caller ignores it.
	}

	end := inner.End.Byte
	for end < template.End.Byte && source[end] != '}' {
		end++
	}

	if end >= template.End.Byte || source[end] != '}' {
		return hcl.Range{}, false //nolint:exhaustruct // the caller ignores it.
	}

	span := inner
	span.Start.Byte = start
	span.End.Byte = end + 1

	return span, true
}

func stripMarkerEdits(source []byte, where site, span hcl.Range) []edit {
	text := sourceText(source, span)

	edits := []edit{}

	if strings.HasPrefix(text, "${~") {
		marker := span
		marker.Start.Byte += len("${")
		marker.End.Byte = marker.Start.Byte + 1
		edits = append(edits, remove(TplStripFlip, where, marker))
	}

	if strings.HasSuffix(text, "~}") {
		marker := span
		marker.Start.Byte = span.End.Byte - len("~}")
		marker.End.Byte = marker.Start.Byte + 1
		edits = append(edits, remove(TplStripFlip, where, marker))
	}

	return edits
}

// minimumElements is the tuple size below which dropping or reversing says
// nothing about the suite.
const minimumElements = 2

func tupleEdits(source []byte, where site, expr *hclsyntax.TupleConsExpr) []edit {
	edits := []edit{replace(CollEmpty, where, expr.SrcRange, "[]")}

	if len(expr.Exprs) < minimumElements {
		return edits
	}

	first := expr.Exprs[0].Range()
	second := expr.Exprs[1].Range()
	edits = append(edits, remove(CollDropFirst, where, spanBetween(first.Start, second.Start)))

	penultimate := expr.Exprs[len(expr.Exprs)-2].Range()
	last := expr.Exprs[len(expr.Exprs)-1].Range()
	edits = append(edits, remove(CollDropLast, where, spanBetween(penultimate.End, last.End)))

	reversal := make([]part, 0, len(expr.Exprs))

	for index, element := range expr.Exprs {
		mirror := expr.Exprs[len(expr.Exprs)-1-index]
		reversal = append(reversal, part{
			rng:  element.Range(),
			text: sourceText(source, mirror.Range()),
		})
	}

	return append(edits, edit{
		operator: CollReverse, parts: reversal,
		site: where.address, resource: where.resource,
	})
}

func spanBetween(start, end hcl.Pos) hcl.Range {
	return hcl.Range{Filename: "", Start: start, End: end}
}

func objectEdits(source []byte, where site, expr *hclsyntax.ObjectConsExpr) []edit {
	edits := make([]edit, 0, 1+len(expr.Items))
	edits = append(edits, replace(CollEmpty, where, expr.SrcRange, "{}"))

	for index, item := range expr.Items {
		start := item.KeyExpr.Range().Start
		end := item.ValueExpr.Range().End

		if index+1 < len(expr.Items) {
			end = expr.Items[index+1].KeyExpr.Range().Start
		}

		entry := spanBetween(start, end)
		if index+1 == len(expr.Items) {
			entry = lineRange(source, entry)
		}

		edits = append(edits, remove(CollDropEntry, where, entry))
	}

	return edits
}

func forEdits(source []byte, where site, expr *hclsyntax.ForExpr) []edit {
	edits := []edit{}

	if expr.CondExpr != nil {
		condition := expr.CondExpr.Range()

		if keyword, found := findToken(source, spanBetween(expr.ValExpr.Range().End, condition.Start), "if"); found {
			edits = append(edits, remove(ForDropIf, where, spanBetween(keyword.Start, condition.End)))
		}

		edits = append(edits, replace(ForNegateIf, where, condition,
			"!("+sourceText(source, condition)+")"))
	}

	if expr.KeyExpr != nil {
		edits = append(edits, edit{
			operator: ForSwapKV,
			parts: []part{
				{rng: expr.KeyExpr.Range(), text: sourceText(source, expr.ValExpr.Range())},
				{rng: expr.ValExpr.Range(), text: sourceText(source, expr.KeyExpr.Range())},
			},
			site: where.address, resource: where.resource,
		})
	}

	if expr.Group {
		tail := spanBetween(expr.ValExpr.Range().End, expr.CloseRange.End)
		if marker, found := findToken(source, tail, "..."); found {
			edits = append(edits, remove(ForDropGrouping, where, marker))
		}
	}

	return edits
}

func indexEdits(where site, expr *hclsyntax.IndexExpr) []edit {
	literal, ok := expr.Key.(*hclsyntax.LiteralValueExpr)
	if !ok || literal.Val.Type() != cty.Number || literal.Val.IsNull() {
		return nil
	}

	return []edit{
		replace(IdxShift, where, literal.SrcRange, shifted(literal.Val.AsBigFloat(), 1)),
	}
}

// traversalEdits shifts a literal index that the parser folded into a
// traversal, which is the form `xs[0]` takes when its base is a reference.
func traversalEdits(where site, expr *hclsyntax.ScopeTraversalExpr) []edit {
	edits := []edit{}

	for _, step := range expr.Traversal {
		index, ok := step.(hcl.TraverseIndex)
		if !ok || index.Key.Type() != cty.Number || index.Key.IsNull() {
			continue
		}

		shifted := shifted(index.Key.AsBigFloat(), 1)
		edits = append(edits, replace(IdxShift, where, index.SrcRange, "["+shifted+"]"))
	}

	return edits
}

func splatEdits(source []byte, where site, expr *hclsyntax.SplatExpr) []edit {
	whole := expr.SrcRange
	marker := expr.MarkerRange

	if marker.Start.Byte < whole.Start.Byte || marker.End.Byte > whole.End.Byte {
		return nil
	}

	text := sourceText(source, whole)
	head := text[:marker.Start.Byte-whole.Start.Byte]
	tail := text[marker.End.Byte-whole.Start.Byte:]

	return []edit{
		replace(SplatFirst, where, whole, "["+head+"[0]"+tail+"]"),
	}
}
