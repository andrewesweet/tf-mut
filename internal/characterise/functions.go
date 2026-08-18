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
	"length":     stdlib.LengthFunc,
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

// canFunction is `can`, whose Terraform semantics fall out of evaluation
// order: an argument that errors — a `regex` that did not match, an index that
// does not exist — fails the whole condition before the call, and the
// evaluator reads a failed condition as unsatisfied. What is left for the
// function itself is the success case, which is always true.
//
//nolint:gochecknoglobals // an immutable function value.
var canFunction = function.New(&function.Spec{ //nolint:exhaustruct // the unset fields are cty's own defaults.
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
	return function.New(&function.Spec{ //nolint:exhaustruct // the unset fields are cty's own defaults.
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
var allTrueFunction = quantifierFunction(true)

//nolint:gochecknoglobals // an immutable function value.
var anyTrueFunction = quantifierFunction(false)

// quantifierFunction builds `alltrue` and `anytrue`. They matter because the
// idiomatic constraint over a collection is a `for` comprehension inside one
// of them, and hclsyntax evaluates the comprehension itself.
func quantifierFunction(universal bool) function.Function {
	return function.New(&function.Spec{ //nolint:exhaustruct // the unset fields are cty's own defaults.
		Params: []function.Parameter{{
			Name: "list", Type: cty.DynamicPseudoType, AllowDynamicType: true,
		}},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return quantify(args[0], universal), nil
		},
	})
}

func quantify(collection cty.Value, universal bool) cty.Value {
	if collection.IsNull() || !collection.IsKnown() || !collection.CanIterateElements() {
		return cty.BoolVal(false)
	}

	result := universal

	for iterator := collection.ElementIterator(); iterator.Next(); {
		_, element := iterator.Element()

		if element.IsNull() || !element.IsKnown() || element.Type() != cty.Bool {
			return cty.BoolVal(false)
		}

		if universal {
			result = result && element.True()
		} else {
			result = result || element.True()
		}
	}

	return cty.BoolVal(result)
}
