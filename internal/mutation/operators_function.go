package mutation

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The curated function catalogue.
//
// Sequencing note from the M2 spec review (M2): this hard-coded high-signal
// list is deliberately M2's, and the general catalogue derived from
// `terraform metadata functions -json` — substitutions generated from arity and
// type compatibility — remains M3. Nothing here consults Terraform's function
// metadata, and the boundary is asserted by a test.
//
//nolint:gochecknoglobals // immutable lookup tables.
var (
	// swappableFunctions are the pairs whose confusion is a real fault.
	swappableFunctions = map[string]string{
		"min": "max", "max": "min",
		"floor": "ceil", "ceil": "floor",
		"upper": "lower", "lower": "upper",
		"startswith": "endswith", "endswith": "startswith",
		"alltrue": "anytrue", "anytrue": "alltrue",
		concatFunction: "setunion", "setunion": concatFunction,
	}

	// reorderableFunctions are those whose argument order encodes precedence.
	reorderableFunctions = map[string]bool{
		"coalesce": true, "merge": true, concatFunction: true,
	}

	// normalisationWrappers change collection semantics in ways that are easy
	// to get wrong and almost never asserted on.
	normalisationWrappers = map[string]bool{
		"distinct": true, "sort": true, "compact": true, "flatten": true, "toset": true,
	}
)

// concatFunction appears in two curated groups, which is why it is named.
const concatFunction = "concat"

// The argument counts the fallback operators require.
const (
	lookupWithDefault = 3
	pairArguments     = 2
)

func functionEdits(source []byte, where site, expr *hclsyntax.FunctionCallExpr) []edit {
	edits := make([]edit, 0, len(expr.Args))
	edits = append(edits, functionSwapEdits(source, where, expr)...)
	edits = append(edits, functionFallbackEdits(source, where, expr)...)
	edits = append(edits, functionExpansionEdits(source, where, expr)...)

	return edits
}

func functionSwapEdits(source []byte, where site, expr *hclsyntax.FunctionCallExpr) []edit {
	edits := []edit{}

	if replacement, found := swappableFunctions[expr.Name]; found {
		name := expr.Range()
		name.End.Byte = name.Start.Byte + len(expr.Name)
		edits = append(edits, replace(FnSwap, where, name, replacement))
	}

	if reorderableFunctions[expr.Name] && len(expr.Args) >= pairArguments {
		edits = append(edits, edit{
			operator: FnArgReorder,
			parts: []part{
				{rng: expr.Args[0].Range(), text: sourceText(source, expr.Args[1].Range())},
				{rng: expr.Args[1].Range(), text: sourceText(source, expr.Args[0].Range())},
			},
			site: where.address, resource: where.resource,
		})
	}

	if normalisationWrappers[expr.Name] && len(expr.Args) == 1 && !expr.ExpandFinal {
		edits = append(edits, replace(FnDropWrapper, where, expr.Range(),
			sourceText(source, expr.Args[0].Range())))
	}

	return edits
}

func functionFallbackEdits(source []byte, where site, expr *hclsyntax.FunctionCallExpr) []edit {
	edits := []edit{}

	switch expr.Name {
	case "lookup":
		if len(expr.Args) == lookupWithDefault {
			edits = append(edits, remove(FnDropDefault, where,
				spanBetween(expr.Args[1].Range().End, expr.Args[2].Range().End)))
		}
	case "try":
		if len(expr.Args) >= pairArguments {
			last := expr.Args[len(expr.Args)-1]
			edits = append(edits,
				remove(FnDropDefault, where, spanBetween(expr.Args[0].Range().End, last.Range().End)),
				replace(FnTryFirst, where, expr.Range(), sourceText(source, last.Range())))
		}
	case "can":
		if len(expr.Args) == 1 {
			edits = append(edits, replace(FnCanTrue, where, expr.Range(), "true"))
		}
	case "join":
		if len(expr.Args) >= pairArguments && isSeparatorLiteral(source, expr.Args[0]) {
			edits = append(edits, replace(FnJoinSep, where, expr.Args[0].Range(), `""`))
		}
	default:
	}

	return edits
}

// isSeparatorLiteral gates FN-JOIN-SEP on evidence that the separator is a
// non-empty literal: emptying an already-empty separator changes nothing, and
// emptying an expression would change what the operator claims to model.
func isSeparatorLiteral(source []byte, expr hclsyntax.Expression) bool {
	template, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok {
		return false
	}

	literal, isLiteral := isStringLiteral(source, template)

	return isLiteral && literal != ""
}

func functionExpansionEdits(source []byte, where site, expr *hclsyntax.FunctionCallExpr) []edit {
	if !expr.ExpandFinal || len(expr.Args) == 0 {
		return nil
	}

	last := expr.Args[len(expr.Args)-1].Range()

	marker, found := findToken(source, spanBetween(last.End, expr.CloseParenRange.End), "...")
	if !found {
		return nil
	}

	return []edit{remove(FnDropExpansion, where, marker)}
}

// CuratedFunctions lists every function name the curated catalogue reaches, so
// that the boundary against M3's metadata-driven catalogue can be asserted
// rather than described.
func CuratedFunctions() []string {
	names := map[string]bool{"lookup": true, "try": true, "can": true, "join": true}
	for name := range swappableFunctions {
		names[name] = true
	}

	for name := range reorderableFunctions {
		names[name] = true
	}

	for name := range normalisationWrappers {
		names[name] = true
	}

	listed := make([]string, 0, len(names))
	for name := range names {
		listed = append(listed, name)
	}

	slices.Sort(listed)

	return listed
}

// isMetaArgument reports the meta-arguments Tier 2 owns, which the expression
// operators must leave alone: `count = 0` reached through NUM-ZERO would bypass
// the multiplicity gate that keeps emptied instance sets out of the Invalid
// pile.
func isMetaArgument(name string) bool {
	return metaArguments[name]
}

// contractBlocks are the blocks whose conditions Tier 3 owns. A negation inside
// one is a contract finding — structurally unassertable, with a negative test
// as its fix — and letting the boolean operators claim it first would relabel
// it as an expression fault.
func isContractBlock(blockType string) bool {
	switch strings.ToLower(blockType) {
	case "validation", "precondition", "postcondition", "assert":
		return true
	default:
		return false
	}
}
