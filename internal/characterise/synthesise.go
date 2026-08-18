package characterise

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// Synthesis is one input's resolution: a value with its provenance, or the
// judgement point that stopped it.
//
// The preference order is the design's, and it never guesses: a default is the
// module's own statement about the input, a mined value is the module's own
// statement about which values it accepts, a typed value is the declared
// type's simplest inhabitant — and anything that does not satisfy the declared
// constraints becomes a TODO carrying the constraint verbatim rather than a
// value that plans and characterises nonsense.
type Synthesis struct {
	// Name is the variable.
	Name string
	// Expression is the rendered Terraform literal, empty where none was found.
	Expression string
	// Provenance names which rung produced the value.
	Provenance report.InputProvenance
	// Assign reports whether the run block has to carry the assignment. A
	// variable resolved from its own default needs none.
	Assign bool
	// Gap is the reason no value was found, empty where one was.
	Gap string
	// Attempted lists the values tried and rejected, in order.
	Attempted []string
	// Constraint is the first unsatisfied validation's verbatim source.
	Constraint string
	// ConstraintRange is where that validation is declared.
	ConstraintRange hcl.Range
}

// Resolved reports a variable the pipeline found a value for.
func (s Synthesis) Resolved() bool {
	return s.Gap == ""
}

// Synthesise resolves one variable.
//
// It is the whole preference pipeline behind one call, and it is exported
// because the corpus measurement runs exactly this pipeline — statically, over
// public modules — to publish the rate at which it produces executable
// scenarios rather than judgement points.
func Synthesise(variable discovery.Block, sources map[string][]byte, answer string) Synthesis {
	result := Synthesis{
		Name: variable.Name, Expression: "", Provenance: "", Assign: false,
		Gap: "", Attempted: []string{}, Constraint: "", ConstraintRange: hcl.Range{}, //nolint:exhaustruct // the empty range.
	}

	if answer != "" {
		return accept(result, answer, variable, sources)
	}

	if _, declared := attributeOf(variable, "default"); declared {
		result.Provenance = report.FromDefault

		return result
	}

	for _, candidate := range mined(variable) {
		if attempt := check(result, candidate, report.FromValidation, variable, sources); attempt.Resolved() {
			return attempt
		}

		result.Attempted = append(result.Attempted, candidate)
	}

	candidate, typed := typedValue(variable)
	if !typed {
		return gap(result, variable, sources,
			"the variable declares no default, no minable validation and no type this "+
				"version synthesises a value for")
	}

	if attempt := check(result, candidate, report.FromType, variable, sources); attempt.Resolved() {
		return attempt
	}

	result.Attempted = append(result.Attempted, candidate)

	return gap(result, variable, sources,
		"no synthesised value satisfied the variable's declared constraints")
}

// accept takes an answer on its own terms.
//
// An answer is not a guess the tool has to justify: it is the judgement the
// tool refused to make, and the thing that verifies it is the real plan, not
// this evaluator. So an answer is refused only where a validation is decidably
// false — the fail-*open* direction, deliberately, because a constraint this
// evaluator cannot decide is one Terraform can, and rejecting the reader's
// answer over it would make the loop unclosable.
func accept(
	result Synthesis,
	answer string,
	variable discovery.Block,
	sources map[string][]byte,
) Synthesis {
	context, ok := bind(answer, variable)
	if !ok {
		return gap(result, variable, sources,
			"the answer is not a constant Terraform expression")
	}

	for _, validation := range variable.Validations {
		if holds, decidable := evaluate(validation, context); decidable && !holds {
			result.Constraint = sourceText(validation, sources)
			result.ConstraintRange = validation.Range
			result.Gap = "the answer does not satisfy this validation"

			return result
		}
	}

	result.Expression = answer
	result.Provenance = report.FromAnswer
	result.Assign = true

	return result
}

// bind parses a candidate and binds it as the variable's value.
func bind(candidate string, variable discovery.Block) (*hcl.EvalContext, bool) {
	expr, diagnostics := hclsyntax.ParseExpression(
		[]byte(candidate), "synthesis", hcl.InitialPos,
	)
	if diagnostics.HasErrors() {
		return nil, false
	}

	value, diagnostics := expr.Value(nil)
	if diagnostics.HasErrors() {
		return nil, false
	}

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			variableRoot: cty.ObjectVal(map[string]cty.Value{variable.Name: value}),
		},
		Functions: validationFunctionTable,
	}, true
}

// check evaluates a candidate against every validation the variable declares.
//
// Statically: the conditions are HCL expressions over `var.<name>` and nothing
// else, so binding the candidate is enough to decide them without planning.
// A condition this evaluator cannot decide is treated as unsatisfied, which is
// the fail-closed direction — it produces a judgement point rather than a
// value nobody checked.
func check(
	result Synthesis,
	candidate string,
	provenance report.InputProvenance,
	variable discovery.Block,
	sources map[string][]byte,
) Synthesis {
	context, ok := bind(candidate, variable)
	if !ok {
		return gap(result, variable, sources,
			"the synthesised value is not a constant Terraform expression")
	}

	for _, validation := range variable.Validations {
		if holds, decidable := evaluate(validation, context); decidable && holds {
			continue
		}

		result.Constraint = sourceText(validation, sources)
		result.ConstraintRange = validation.Range
		result.Gap = "the candidate does not satisfy this validation"

		return result
	}

	result.Expression = candidate
	result.Provenance = provenance
	result.Assign = true

	return result
}

// evaluate decides one validation for the bound candidate, and says whether it
// could decide it at all.
//
// The two callers want opposite things from an undecidable condition, which is
// why the answer is a pair rather than a boolean: a synthesised value must be
// proven acceptable, and a supplied answer must be proven unacceptable.
func evaluate(validation discovery.Validation, context *hcl.EvalContext) (holds, decidable bool) {
	value, diagnostics := validation.Condition.Value(context)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsKnown() {
		return false, false
	}

	converted, err := convert.Convert(value, cty.Bool)
	if err != nil {
		return false, false
	}

	return converted.True(), true
}

// gap records the judgement point, with the first constraint the reader would
// have to satisfy attached verbatim.
func gap(
	result Synthesis,
	variable discovery.Block,
	sources map[string][]byte,
	reason string,
) Synthesis {
	result.Gap = reason
	result.Expression = ""
	result.Assign = false

	if len(variable.Validations) > 0 && result.Constraint == "" {
		result.Constraint = sourceText(variable.Validations[0], sources)
		result.ConstraintRange = variable.Validations[0].Range
	}

	return result
}

// sourceText quotes a validation condition verbatim, which is what a TODO has
// to carry: a paraphrase of a constraint is a constraint nobody can satisfy.
func sourceText(validation discovery.Validation, sources map[string][]byte) string {
	content, found := sources[validation.File]
	if !found {
		return ""
	}

	start, end := validation.Range.Start.Byte, validation.Range.End.Byte
	if start < 0 || end > len(content) || start >= end {
		return ""
	}

	return strings.TrimSpace(string(content[start:end]))
}

// mined reads the values a variable's validations name outright.
//
// Two forms carry a legal value in the expression itself: membership of a
// literal list, and equality with a literal. Everything else — regular
// expressions, length bounds, `alltrue` over a comprehension — states a
// property rather than a value, and mining a property is guessing.
func mined(variable discovery.Block) []string {
	candidates := make([]string, 0, len(variable.Validations))

	for _, validation := range variable.Validations {
		candidates = append(candidates, mineExpression(validation.Condition, variable.Name)...)
	}

	return candidates
}

func mineExpression(expr hclsyntax.Expression, name string) []string {
	switch typed := expr.(type) {
	case *hclsyntax.FunctionCallExpr:
		return mineContains(typed, name)
	case *hclsyntax.BinaryOpExpr:
		return mineEquality(typed, name)
	default:
		return nil
	}
}

// mineContains reads `contains([...], var.x)`.
func mineContains(call *hclsyntax.FunctionCallExpr, name string) []string {
	const containsArguments = 2

	if call.Name != "contains" || len(call.Args) != containsArguments {
		return nil
	}

	if !readsVariable(call.Args[1], name) {
		return nil
	}

	value, diagnostics := call.Args[0].Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() {
		return nil
	}

	if !value.CanIterateElements() {
		return nil
	}

	candidates := []string{}

	for iterator := value.ElementIterator(); iterator.Next(); {
		_, element := iterator.Element()
		candidates = append(candidates, renderValue(element))
	}

	return candidates
}

// mineEquality reads `var.x == "literal"` and the conjunctions around it.
func mineEquality(operation *hclsyntax.BinaryOpExpr, name string) []string {
	if operation.Op == hclsyntax.OpLogicalAnd {
		return append(mineExpression(operation.LHS, name), mineExpression(operation.RHS, name)...)
	}

	if operation.Op != hclsyntax.OpEqual {
		return nil
	}

	literal := operation.RHS
	if readsVariable(operation.RHS, name) {
		literal = operation.LHS
	} else if !readsVariable(operation.LHS, name) {
		return nil
	}

	value, diagnostics := literal.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() {
		return nil
	}

	return []string{renderValue(value)}
}

// readsVariable reports whether an expression is exactly `var.<name>`.
func readsVariable(expr hclsyntax.Expression, name string) bool {
	read, ok := variableName(expr)

	return ok && read == name
}

// renderValue renders a cty value back into Terraform syntax.
func renderValue(value cty.Value) string {
	return strings.TrimSpace(string(hclwrite.TokensForValue(value).Bytes()))
}

// typedValue synthesises the declared type's simplest inhabitant.
func typedValue(variable discovery.Block) (string, bool) {
	attribute, declared := attributeOf(variable, "type")
	if !declared {
		// An untyped variable accepts anything, and a string is the value
		// Terraform itself defaults such a variable to.
		return placeholderString, true
	}

	return synthesiseType(attribute.Expr, 0)
}

// placeholderString is the synthesised value of an unconstrained string.
const placeholderString = `"tfmut-placeholder"`

// nestingLimit bounds the recursion through object and collection types. A
// deeper type is a judgement point rather than a value nobody would recognise.
const nestingLimit = 6

// synthesiseType walks a type expression, which Terraform writes as a call
// tree — `list(object({ name = string }))` — rather than as a value.
func synthesiseType(expr hclsyntax.Expression, depth int) (string, bool) {
	if depth > nestingLimit {
		return "", false
	}

	switch typed := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		return primitiveValue(typed.Traversal.RootName())
	case *hclsyntax.FunctionCallExpr:
		return collectionValue(typed, depth)
	case *hclsyntax.ObjectConsExpr:
		return objectValue(typed, depth)
	default:
		return "", false
	}
}

func primitiveValue(name string) (string, bool) {
	switch name {
	case "string", "any":
		return placeholderString, true
	case "number":
		return "1", true
	case "bool":
		return "true", true
	default:
		return "", false
	}
}

func collectionValue(call *hclsyntax.FunctionCallExpr, depth int) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}

	switch call.Name {
	case "list", "set", "tuple":
		element, ok := synthesiseType(call.Args[0], depth+1)
		if !ok {
			return "", false
		}

		return "[" + element + "]", true
	case "map":
		element, ok := synthesiseType(call.Args[0], depth+1)
		if !ok {
			return "", false
		}

		return "{ tfmut = " + element + " }", true
	case "object":
		return synthesiseType(call.Args[0], depth+1)
	default:
		// `optional` included: an optional attribute is omitted rather than
		// filled, because the module states that it has a meaning without one.
		return "", false
	}
}

func objectValue(object *hclsyntax.ObjectConsExpr, depth int) (string, bool) {
	fields := []string{}

	for _, item := range object.Items {
		name, ok := objectKey(item.KeyExpr)
		if !ok {
			return "", false
		}

		value, synthesised := synthesiseType(item.ValueExpr, depth+1)
		if !synthesised {
			// Optional attributes are legitimately absent; anything else this
			// version cannot synthesise makes the whole object a judgement
			// point rather than a half-filled guess.
			if optionalAttribute(item.ValueExpr) {
				continue
			}

			return "", false
		}

		fields = append(fields, name+" = "+value)
	}

	if len(fields) == 0 {
		return "{}", true
	}

	return "{ " + strings.Join(fields, ", ") + " }", true
}

func objectKey(expr hclsyntax.Expression) (string, bool) {
	if wrapped, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok {
		expr = wrapped.Wrapped
	}

	switch typed := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		return typed.Traversal.RootName(), true
	case *hclsyntax.TemplateExpr:
		value, diagnostics := typed.Value(nil)
		if diagnostics.HasErrors() || value.Type() != cty.String {
			return "", false
		}

		return value.AsString(), true
	default:
		return "", false
	}
}

func optionalAttribute(expr hclsyntax.Expression) bool {
	call, ok := expr.(*hclsyntax.FunctionCallExpr)

	return ok && call.Name == "optional"
}
