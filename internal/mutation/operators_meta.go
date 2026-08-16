package mutation

import (
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// The meta-argument names Tier 2 fires on.
const (
	countArgument     = "count"
	forEachArgument   = "for_each"
	dependsOnArgument = "depends_on"
	providerArgument  = "provider"
)

// metaEdits offers the Tier 2 operators a meta-argument assignment.
//
// The multiplicity gate is shared with EXT-RESOURCE-DELETE and for the same
// reason: emptying an instance set that a bare or exact-index consumer reads is
// statically doomed or errors at evaluation, and neither outcome grades the
// test suite.
func (g Generator) metaEdits(
	module discovery.Module,
	source []byte,
	where site,
	attribute *hclsyntax.Attribute,
) []edit {
	switch where.attributeName {
	case countArgument:
		if where.dynamic {
			return nil
		}

		return g.countEdits(module, where, attribute)
	case forEachArgument:
		if where.dynamic {
			return dynamicEdits(where, attribute)
		}

		return g.forEachEdits(module, where, attribute)
	case dependsOnArgument:
		return []edit{remove(DependsDrop, where, lineRange(source, attribute.SrcRange))}
	case providerArgument:
		return g.providerEdits(where, attribute)
	default:
		return nil
	}
}

func (g Generator) countEdits(module discovery.Module, where site, attribute *hclsyntax.Attribute) []edit {
	edits := []edit{}

	if g.consumersTolerateEmpty(module, where.resource) {
		edits = append(edits, replace(CountZero, where, attribute.Expr.Range(), "0"))
	}

	literal, ok := attribute.Expr.(*hclsyntax.LiteralValueExpr)
	if !ok || literal.Val.Type() != cty.Number || literal.Val.IsNull() {
		return edits
	}

	count, _ := literal.Val.AsBigFloat().Int64()
	if count != 1 {
		edits = append(edits, replace(CountOne, where, attribute.Expr.Range(), "1"))
	}

	edits = append(edits, replace(CountOffByOne, where, attribute.Expr.Range(),
		strconv.FormatInt(count+1, 10)))

	if count > 1 {
		edits = append(edits, replace(CountOffByOne, where, attribute.Expr.Range(),
			strconv.FormatInt(count-1, 10)))
	}

	return edits
}

func (g Generator) forEachEdits(
	module discovery.Module,
	where site,
	attribute *hclsyntax.Attribute,
) []edit {
	edits := []edit{}

	if g.consumersTolerateEmpty(module, where.resource) {
		edits = append(edits, replace(ForEachEmpty, where, attribute.Expr.Range(), "{}"))
	}

	return edits
}

func dynamicEdits(where site, attribute *hclsyntax.Attribute) []edit {
	return []edit{replace(DynamicZero, where, attribute.Expr.Range(), "[]")}
}

// providerEdits swaps a provider alias for another declared alias of the same
// provider — and only where the two have identical mock status.
//
// The guard is load-bearing rather than tidy: a swap that moved a resource from
// a mocked provider to an unmocked one would execute against real
// infrastructure past a safety gate that already ran (M2 spec review, M3).
func (g Generator) providerEdits(where site, attribute *hclsyntax.Attribute) []edit {
	traversal, ok := attribute.Expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(traversal.Traversal) != referenceParts {
		return nil
	}

	name := traversal.Traversal.RootName()

	current, ok := traversal.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return nil
	}

	edits := []edit{}

	for _, candidate := range g.Configuration.ProviderAliases(name) {
		if candidate == current.Name {
			continue
		}

		if g.Configuration.AliasMocked(name, candidate) != g.Configuration.AliasMocked(name, current.Name) {
			continue
		}

		edits = append(edits, replace(ProviderAliasSwap, where, attribute.Expr.Range(),
			name+"."+candidate))
	}

	return edits
}

// referenceParts is the traversal length of `<provider>.<alias>`.
const referenceParts = 2

// contractEdits offers the Tier 3 operators an attribute inside a contract
// block: a validation, a precondition, a postcondition or a check assertion.
//
// VAR-VALIDATION-WEAKEN emits exactly `condition = can(var.<name>)`. The
// obvious `condition = true` is 100% Invalid — Terraform rejects a validation
// condition that does not refer to its own variable — and the catalogue records
// that failure history because the operator was written the wrong way once.
func contractEdits(source []byte, where site, attribute *hclsyntax.Attribute) []edit {
	if attribute.Name != "condition" {
		return nil
	}

	condition := attribute.Expr.Range()
	negated := replace(negateOperator(where), where, condition, "!("+sourceText(source, condition)+")")

	if where.variable == "" || !strings.Contains(where.address, ".validation.") {
		return []edit{negated}
	}

	return []edit{
		negated,
		replace(VarValidationWeaken, where, condition, "can(var."+where.variable+")"),
	}
}

func negateOperator(where site) Operator {
	switch {
	case where.variable != "" && strings.Contains(where.address, ".validation."):
		return VarValidationNegate
	case where.kind == checkKind:
		return CheckNegate
	default:
		return PrePostNegate
	}
}

// variableEdits offers the Tier 3 operators the attributes of a variable
// declaration: the module's public contract.
func variableEdits(source []byte, where site, attribute *hclsyntax.Attribute, nullable bool) []edit {
	switch attribute.Name {
	case "default":
		return defaultEdits(source, where, attribute, nullable)
	case "nullable":
		return flagEdits(VarNullableFlip, where, attribute, false)
	case "sensitive":
		return flagEdits(VarSensitiveFlip, where, attribute, true)
	case "type":
		return optionalDefaultEdits(where, attribute)
	default:
		return nil
	}
}

func defaultEdits(source []byte, where site, attribute *hclsyntax.Attribute, nullable bool) []edit {
	edits := []edit{remove(VarDefaultRemove, where, lineRange(source, attribute.SrcRange))}

	if nullable {
		edits = append(edits, replace(VarDefaultNull, where, attribute.Expr.Range(), "null"))
	}

	if alternative, ok := typeAppropriateAlternative(source, attribute.Expr); ok {
		edits = append(edits, replace(VarDefaultChange, where, attribute.Expr.Range(), alternative))
	}

	return edits
}

// typeAppropriateAlternative is the other value of the default's own type,
// which keeps the mutant type-preserving where the type is decidable and emits
// nothing where it is not.
func typeAppropriateAlternative(source []byte, expr hclsyntax.Expression) (string, bool) {
	switch typed := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		if typed.Val.Type() == cty.Bool && !typed.Val.IsNull() {
			return strconv.FormatBool(!typed.Val.True()), true
		}

		if typed.Val.Type() == cty.Number && !typed.Val.IsNull() {
			return shifted(typed.Val.AsBigFloat(), 1), true
		}
	case *hclsyntax.TemplateExpr:
		if literal, ok := isStringLiteral(source, typed); ok && literal != "" {
			return `""`, true
		}
	case *hclsyntax.TupleConsExpr:
		if len(typed.Exprs) > 0 {
			return "[]", true
		}
	case *hclsyntax.ObjectConsExpr:
		if len(typed.Items) > 0 {
			return "{}", true
		}
	default:
	}

	return "", false
}

// flagEdits flips a boolean flag, but only away from the value that makes the
// flag mean something: flipping `sensitive = false` to `true` models no fault.
func flagEdits(operator Operator, where site, attribute *hclsyntax.Attribute, from bool) []edit {
	literal, ok := attribute.Expr.(*hclsyntax.LiteralValueExpr)
	if !ok || literal.Val.Type() != cty.Bool || literal.Val.IsNull() || literal.Val.True() != from {
		return nil
	}

	return []edit{replace(operator, where, attribute.Expr.Range(), strconv.FormatBool(!from))}
}

// optionalDefaultEdits drops the default from an `optional(type, default)`
// attribute of an object type constraint.
func optionalDefaultEdits(where site, attribute *hclsyntax.Attribute) []edit {
	edits := []edit{}

	walkExpressionTree(attribute.Expr, func(expr hclsyntax.Expression) {
		call, ok := expr.(*hclsyntax.FunctionCallExpr)
		if !ok || call.Name != "optional" || len(call.Args) != pairArguments {
			return
		}

		edits = append(edits, remove(VarOptionalDefaultDrop, where,
			spanBetween(call.Args[0].Range().End, call.Args[1].Range().End)))
	})

	return edits
}

// outputEdits offers the Tier 3 operators an output declaration's attributes.
func outputEdits(where site, attribute *hclsyntax.Attribute) []edit {
	if attribute.Name != "sensitive" {
		return nil
	}

	return flagEdits(OutSensitiveFlip, where, attribute, true)
}

// blockRemovalEdits deletes a whole contract block, which is the mutation that
// asks whether anything exercises the contract at all.
func blockRemovalEdits(source []byte, where site, block *hclsyntax.Block) []edit {
	operator, applies := removalOperator(where, block)
	if !applies {
		return nil
	}

	span := hcl.RangeBetween(block.TypeRange, block.CloseBraceRange)

	return []edit{remove(operator, where, lineRange(source, span))}
}

func removalOperator(where site, block *hclsyntax.Block) (Operator, bool) {
	switch strings.ToLower(block.Type) {
	case "validation":
		if where.variable == "" {
			return "", false
		}

		return VarValidationRemove, true
	case "precondition", "postcondition":
		return PrePostRemove, true
	case "assert":
		if where.kind != checkKind {
			return "", false
		}

		return CheckRemove, true
	default:
		return "", false
	}
}

// forEachToCountEdits rewrites a set-valued `for_each` as the equivalent
// `count`, together with every `each.*` reference in the block body.
//
// The rewrite is coordinated by necessity: `count` supplies `count.index` and
// no key, so a body left referring to `each.value` would not parse. It fires
// only where the collection is a set built from an indexable list — `toset(x)`
// or a tuple literal — because a map `for_each` has keys that `count` cannot
// reproduce, and pretending otherwise would change the fault being modelled.
func forEachToCountEdits(source []byte, where site, block *hclsyntax.Block) []edit {
	attribute, found := block.Body.Attributes[forEachArgument]
	if !found {
		return nil
	}

	list, ok := indexableCollection(source, attribute.Expr)
	if !ok {
		return nil
	}

	parts := []part{{
		rng:  hcl.RangeBetween(attribute.NameRange, attribute.Expr.Range()),
		text: "count = length(" + list + ")",
	}}

	for _, reference := range eachReferences(block.Body) {
		parts = append(parts, part{rng: reference, text: list + "[count.index]"})
	}

	return []edit{{
		operator: ForEachToCount, parts: parts,
		site: where.address + "." + forEachArgument, resource: where.resource,
	}}
}

// indexableCollection returns the list expression underlying a set-valued
// for_each, and whether the form is one the rewrite can reproduce.
func indexableCollection(source []byte, expr hclsyntax.Expression) (string, bool) {
	switch typed := expr.(type) {
	case *hclsyntax.FunctionCallExpr:
		if typed.Name != "toset" || len(typed.Args) != 1 {
			return "", false
		}

		return sourceText(source, typed.Args[0].Range()), true
	case *hclsyntax.TupleConsExpr:
		return sourceText(source, typed.Range()), true
	default:
		return "", false
	}
}

// eachReferences lists every `each.key` and `each.value` traversal in a body.
func eachReferences(body *hclsyntax.Body) []hcl.Range {
	ranges := []hcl.Range{}

	for _, attribute := range body.Attributes {
		walkExpressionTree(attribute.Expr, func(expr hclsyntax.Expression) {
			traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr)
			if !ok || traversal.Traversal.RootName() != "each" {
				return
			}

			ranges = append(ranges, traversal.Range())
		})
	}

	for _, nested := range body.Blocks {
		ranges = append(ranges, eachReferences(nested.Body)...)
	}

	return ranges
}
