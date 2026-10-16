package characterise

import (
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// The Terraform functions a validation condition may call that this evaluator
// implements.
//
// The set is deliberately small and every omission fails closed: a condition
// calling something absent from it cannot be evaluated, `satisfied` reads that
// as unsatisfied, and the variable becomes a judgement point rather than a
// value the tool pretended to check. Six of the eleven come straight from
// cty's own standard library, which is the same implementation Terraform's
// own functions are built on.
//
//nolint:gochecknoglobals // an immutable function table.
var validationFunctionTable = map[string]function.Function{
	"contains":   stdlib.ContainsFunc,
	"length":     lengthFunction,
	"regex":      stdlib.RegexFunc,
	"regexall":   stdlib.RegexAllFunc,
	"lower":      stdlib.LowerFunc,
	"upper":      stdlib.UpperFunc,
	"can":        canFunction,
	"startswith": startsWithFunction,
	"endswith":   endsWithFunction,
	"alltrue":    allTrueFunction,
	"anytrue":    anyTrueFunction,
}

// lengthFunction is Terraform's `length`, which is two functions in cty's
// standard library and one in Terraform's.
//
// `length(var.x) > n` over a *string* is among the commonest validation
// idioms there is, and cty's `LengthFunc` accepts only collections: wiring it
// straight through left every such condition undecidable, so every variable
// constrained that way became a judgement point nobody needed to answer. The
// failure was silent in exactly the way a wrong function table always is —
// nothing errored, the tool simply refused to synthesise values it could have.
//
//nolint:gochecknoglobals // an immutable function value.
var lengthFunction = function.New(&function.Spec{ // the unset fields are cty's own defaults.
	Params: []function.Parameter{{
		Name: "value", Type: cty.DynamicPseudoType, AllowDynamicType: true,
	}},
	Type: function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		if args[0].Type() == cty.String {
			return stdlib.Strlen(args[0])
		}

		return stdlib.Length(args[0])
	},
})

// canFunction is `can`, whose Terraform semantics fall out of evaluation
// order: an argument that errors — a `regex` that did not match, an index that
// does not exist — fails the whole condition before the call, and the
// evaluator reads a failed condition as unsatisfied. What is left for the
// function itself is the success case, which is always true.
//
//nolint:gochecknoglobals // an immutable function value.
var canFunction = function.New(&function.Spec{ // the unset fields are cty's own defaults.
	Params: []function.Parameter{{
		Name: "expression", Type: cty.DynamicPseudoType,
		AllowNull: true, AllowUnknown: true, AllowDynamicType: true,
	}},
	Type: function.StaticReturnType(cty.Bool),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		return cty.BoolVal(args[0].IsKnown()), nil
	},
})

//nolint:gochecknoglobals // an immutable function value.
var startsWithFunction = affixFunction(strings.HasPrefix)

//nolint:gochecknoglobals // an immutable function value.
var endsWithFunction = affixFunction(strings.HasSuffix)

// affixFunction builds `startswith` and `endswith`, which differ only in which
// end of the string they read.
func affixFunction(matches func(text, affix string) bool) function.Function {
	return function.New(&function.Spec{ // the unset fields are cty's own defaults.
		Params: []function.Parameter{
			{Name: "text", Type: cty.String},
			{Name: "affix", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.BoolVal(matches(args[0].AsString(), args[1].AsString())), nil
		},
	})
}

//nolint:gochecknoglobals // an immutable function value.
var allTrueFunction = allTrueQuantifier()

//nolint:gochecknoglobals // an immutable function value.
var anyTrueFunction = anyTrueQuantifier()

// allTrueQuantifier builds `alltrue` and anyTrueQuantifier builds `anytrue`.
// They matter because the idiomatic constraint over a collection is a `for`
// comprehension inside one of them, and hclsyntax evaluates the comprehension
// itself. Each names its own fold, so no boolean selects between them.
func allTrueQuantifier() function.Function {
	return quantifierFunction(quantifyAll)
}

func anyTrueQuantifier() function.Function {
	return quantifierFunction(quantifyAny)
}

// quantifierFunction is the shared shape of `alltrue` and `anytrue`: one
// dynamically-typed list in, one boolean out. The fold differs, so each
// quantifier passes its own implementation.
func quantifierFunction(fold func(cty.Value) cty.Value) function.Function {
	return function.New(&function.Spec{ // the unset fields are cty's own defaults.
		Params: []function.Parameter{{
			Name: "list", Type: cty.DynamicPseudoType, AllowDynamicType: true,
		}},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return fold(args[0]), nil
		},
	})
}

// booleanElements reads the boolean elements of a collection for the
// quantifiers. Anything else — null, unknown, uniterable, or an element
// that is not a known boolean — is undecidable, which the quantifiers read
// as false.
func booleanElements(collection cty.Value) ([]bool, bool) {
	if collection.IsNull() || !collection.IsKnown() || !collection.CanIterateElements() {
		return nil, false
	}

	elements := []bool{}

	for iterator := collection.ElementIterator(); iterator.Next(); {
		_, element := iterator.Element()

		if element.IsNull() || !element.IsKnown() || element.Type() != cty.Bool {
			return nil, false
		}

		elements = append(elements, element.True())
	}

	return elements, true
}

func quantifyAll(collection cty.Value) cty.Value {
	elements, ok := booleanElements(collection)
	if !ok {
		return cty.BoolVal(false)
	}

	for _, element := range elements {
		if !element {
			return cty.BoolVal(false)
		}
	}

	return cty.BoolVal(true)
}

func quantifyAny(collection cty.Value) cty.Value {
	elements, ok := booleanElements(collection)
	if !ok {
		return cty.BoolVal(false)
	}

	for _, element := range elements {
		if element {
			return cty.BoolVal(true)
		}
	}

	return cty.BoolVal(false)
}
