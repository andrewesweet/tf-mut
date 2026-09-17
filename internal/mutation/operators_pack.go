package mutation

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// packEdits offers every selected pack entry the attribute in front of it.
//
// The site rule is the contract's: a top-level argument assignment in a
// `resource` body of the entry's resource type whose value is a single literal
// token equal to `from` after HCL literal decoding. Nested blocks, dynamic
// bodies, meta-arguments, data bodies and string-internal structure are out of
// M5's scope, so an entry naming one of them simply finds no site here. The
// evidence rule is the schema's: the loaded provider schema describes the
// attribute on that resource type, and the entry's `to` is of the declared
// type — `dynamic` accepts any literal kind, a concrete type must match.
func (g Generator) packEdits(where site, attribute *hclsyntax.Attribute) []edit {
	if len(g.Packs) == 0 || where.kind != resourceKind || where.nested || isMetaArgument(where.attributeName) {
		return nil
	}

	value, literal := singleLiteral(attribute.Expr)
	if !literal {
		return nil
	}

	edits := []edit{}

	for _, pack := range g.Packs {
		for _, entry := range pack.Entries {
			if entry.ResourceType != where.blockType || entry.Attribute != where.attributeName {
				continue
			}

			if !sameLiteral(value, entry.From) || !g.schemaAdmits(where, entry) {
				continue
			}

			rewrite := replace(entry.Operator(), where, attribute.Expr.Range(),
				string(hclwrite.TokensForValue(entry.To).Bytes()))
			rewrite.pack = pack.Name
			rewrite.entry = entry.ID

			edits = append(edits, rewrite)
		}
	}

	return edits
}

// singleLiteral decodes an expression that is exactly one literal token: a
// bare bool or number, or a quoted string with no interpolation.
func singleLiteral(expr hclsyntax.Expression) (cty.Value, bool) {
	switch typed := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		if typed.Val.IsNull() || !typed.Val.IsKnown() {
			return cty.NilVal, false
		}

		return typed.Val, true
	case *hclsyntax.TemplateExpr:
		if !typed.IsStringLiteral() {
			return cty.NilVal, false
		}

		value, diagnostics := typed.Value(nil)
		if diagnostics.HasErrors() {
			return cty.NilVal, false
		}

		return value, true
	default:
		return cty.NilVal, false
	}
}

// sameLiteral compares two literals of the same kind by value.
func sameLiteral(left, right cty.Value) bool {
	return left.Type().Equals(right.Type()) && left.Equals(right).True()
}

// schemaAdmits is the evidence rule: the attribute is described on the
// resource type, and `to` is of the declared type.
func (g Generator) schemaAdmits(where site, entry PackEntry) bool {
	declared, described := g.Schemas.Describes(where.kind, where.blockType, where.attributeName)
	if !described {
		return false
	}

	return declared == cty.DynamicPseudoType || declared.Equals(entry.To.Type())
}

// unmatchedEntries lists every selected entry that produced no mutant, in
// pack then entry order.
func (g Generator) unmatchedEntries(mutants []Mutant) []Origin {
	matched := map[Origin]bool{}

	for _, mutant := range mutants {
		for _, origin := range mutant.Origins {
			matched[origin] = true
		}
	}

	unmatched := []Origin{}

	for _, pack := range g.Packs {
		for _, entry := range pack.Entries {
			origin := Origin{Operator: entry.Operator(), Pack: pack.Name, Entry: entry.ID}
			if !matched[origin] {
				unmatched = append(unmatched, origin)
			}
		}
	}

	return unmatched
}
