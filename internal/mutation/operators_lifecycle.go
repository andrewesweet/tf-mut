package mutation

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The lifecycle arguments the admitted Tier 4 operators fire on. Every other
// attribute inside a lifecycle block stays dropped: admission is per operator
// and carried by its M5-0.1 kill witness, not per block.
const (
	ignoreChangesArgument      = "ignore_changes"
	replaceTriggeredByArgument = "replace_triggered_by"

	// allKeyword is Terraform's ignore_changes wildcard. It parses as a bare
	// traversal root, not a keyword, so "already all" is decided structurally.
	allKeyword = "all"
)

// lifecycleEdits rewrites one lifecycle attribute into the admitted Tier 4
// mutants. `create_before_destroy` and `prevent_destroy` keep the pre-M5a
// drop: their design-table rows carry no kill witness under shapes (a)–(e).
func lifecycleEdits(source []byte, where site, attribute *hclsyntax.Attribute) []edit {
	switch attribute.Name {
	case ignoreChangesArgument:
		edits := entryDropEdits(LCIgnoreDrop, source, where, attribute)

		if all, widened := ignoreAllEdit(where, attribute); widened {
			edits = append(edits, all)
		}

		return edits
	case replaceTriggeredByArgument:
		return entryDropEdits(LCReplaceTriggerDrop, source, where, attribute)
	default:
		return nil
	}
}

// entryDropEdits emits one mutant per tuple entry: a lone entry's removal
// takes the whole argument line — the form the M5-0.1 witnesses recorded,
// rather than a list whose emptiness models no fault — and an entry among
// several loses itself and the separator that follows it, so the remainder
// stays a list.
func entryDropEdits(operator Operator, source []byte, where site, attribute *hclsyntax.Attribute) []edit {
	tuple, ok := attribute.Expr.(*hclsyntax.TupleConsExpr)
	if !ok || len(tuple.Exprs) == 0 {
		return nil
	}

	if len(tuple.Exprs) == 1 {
		return []edit{remove(operator, where, lineRange(source, attribute.Range()))}
	}

	edits := make([]edit, 0, len(tuple.Exprs))

	for index := range tuple.Exprs {
		var rng hcl.Range
		if index+1 < len(tuple.Exprs) {
			rng = spanBetween(tuple.Exprs[index].Range().Start, tuple.Exprs[index+1].Range().Start)
		} else {
			rng = spanBetween(tuple.Exprs[index-1].Range().End, tuple.Exprs[index].Range().End)
		}

		edits = append(edits, remove(operator, where, rng))
	}

	return edits
}

// ignoreAllEdit widens a non-empty ignore_changes list to the all keyword.
// The expression is already `all`, or carries no entry to widen, the operator
// models no fault and stays silent.
func ignoreAllEdit(where site, attribute *hclsyntax.Attribute) (edit, bool) {
	tuple, ok := attribute.Expr.(*hclsyntax.TupleConsExpr)
	if !ok || len(tuple.Exprs) == 0 {
		return edit{}, false //nolint:exhaustruct // discarded by the caller.
	}

	return replace(LCIgnoreAll, where, tuple.SrcRange, allKeyword), true
}
