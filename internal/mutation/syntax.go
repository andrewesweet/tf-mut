package mutation

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// generateSyntax produces the Tier 1 to Tier 3 population of one module by
// rewriting byte ranges of its source files.
//
// Every emitted mutant is re-parsed before it is kept. A mutant that does not
// parse is a defect in the operator rather than a finding about the module, and
// silently executing it would spend a Terraform run to learn that.
func (g Generator) generateSyntax(
	module discovery.Module,
	sources map[string]sourceFile,
) ([]Mutant, []string) {
	mutants := []Mutant{}
	rejected := map[Operator]int{}

	for _, path := range module.Files {
		body, parsed := module.Bodies[path]

		source, known := sources[path]
		if !parsed || !known {
			continue
		}

		for _, current := range g.fileEdits(module, source.content, body) {
			mutated := current.apply(source.content)
			if mutated == nil || !parses(mutated, path) {
				rejected[current.operator]++

				continue
			}

			mutants = append(mutants, current.mutant(module, source, mutated))
		}
	}

	return mutants, describeRejections(rejected)
}

func parses(source []byte, path string) bool {
	_, diagnostics := hclsyntax.ParseConfig(source, path, hcl.InitialPos)

	return !diagnostics.HasErrors()
}

func describeRejections(rejected map[Operator]int) []string {
	if len(rejected) == 0 {
		return nil
	}

	described := make([]string, 0, len(rejected))
	for operator, count := range rejected {
		described = append(described, string(operator))
		_ = count
	}

	return []string{"discarded unparseable mutants from operator(s): " +
		strings.Join(sortedStrings(described), ", ") +
		"; this is an operator defect, not a finding about the module"}
}

func sortedStrings(values []string) []string {
	for index := 1; index < len(values); index++ {
		for back := index; back > 0 && values[back] < values[back-1]; back-- {
			values[back], values[back-1] = values[back-1], values[back]
		}
	}

	return values
}

// mutant converts an edit into the population entry the engine executes.
func (e edit) mutant(module discovery.Module, source sourceFile, mutated []byte) Mutant {
	span := e.span()

	return Mutant{
		ID: identify(e.operator, source.rel, e.site,
			e.original(source.content), e.replacement()),
		Operator:  e.operator,
		ModuleRel: module.Rel,
		File:      source.rel,
		Site:      e.site,
		Resource:  e.resource,
		Range:     span,
		Diff:      UnifiedDiff(source.rel, source.content, mutated),
		Mutated:   mutated,
	}
}

// fileEdits offers every operator every construct of one module file.
func (g Generator) fileEdits(module discovery.Module, source []byte, body *hclsyntax.Body) []edit {
	edits := []edit{}

	walkModuleFile(body,
		func(where site, attribute *hclsyntax.Attribute) {
			edits = append(edits, g.attributeEdits(module, source, where, attribute)...)
		},
		func(where site, block *hclsyntax.Block) {
			edits = append(edits, g.blockEdits(source, where, block)...)
		})

	return edits
}

func (g Generator) attributeEdits(
	module discovery.Module,
	source []byte,
	where site,
	attribute *hclsyntax.Attribute,
) []edit {
	// Tier 4 owns the lifecycle settings, and this milestone does not ship it.
	// A precondition inside the same block is Tier 3's and passes through.
	if where.lifecycle && !where.contract {
		return nil
	}

	if where.contract {
		return contractEdits(source, where, attribute)
	}

	if isMetaArgument(where.attributeName) {
		return g.metaEdits(module, source, where, attribute)
	}

	if where.kind == variableKind {
		return variableEdits(source, where, attribute, nullableVariable(where, module))
	}

	if where.kind == outputKind && attribute.Name != "value" {
		return outputEdits(where, attribute)
	}

	return g.expressionOperators(source, where, attribute)
}

// expressionOperators walks the whole expression tree of an assignment.
func (g Generator) expressionOperators(source []byte, where site, attribute *hclsyntax.Attribute) []edit {
	edits := []edit{}

	walkExpressionTree(attribute.Expr, func(expr hclsyntax.Expression) {
		edits = append(edits, expressionEdits(source, where, expr)...)
	})

	if injected, ok := g.nullInjection(where, attribute); ok {
		edits = append(edits, injected)
	}

	return edits
}

// nullInjection replaces a schema-optional argument's value with null.
//
// It is gated on the same schema evidence as EXT-ATTR-DELETE, for the same
// reason: nulling a required argument is statically doomed, and a doomed mutant
// grades nothing.
func (g Generator) nullInjection(where site, attribute *hclsyntax.Attribute) (edit, bool) {
	if where.kind != resourceKind && where.kind != dataKind {
		return edit{}, false //nolint:exhaustruct // discarded by the caller.
	}

	if literal, ok := attribute.Expr.(*hclsyntax.LiteralValueExpr); ok && literal.Val.IsNull() {
		return edit{}, false //nolint:exhaustruct // discarded by the caller.
	}

	optional, known := g.Schemas.Optionality(where.kind, where.blockType, attribute.Name)
	if !known || !optional {
		return edit{}, false //nolint:exhaustruct // discarded by the caller.
	}

	return replace(NullInject, where, attribute.Expr.Range(), "null"), true
}

// nullableVariable reports whether a variable accepts null, which is what
// decides whether VAR-DEFAULT-NULL can fire without being doomed.
func nullableVariable(where site, module discovery.Module) bool {
	declaration, found := module.VariableByName(where.variable)
	if !found {
		return true
	}

	for _, attribute := range declaration.Attributes {
		if attribute.Name != "nullable" {
			continue
		}

		return literalBool(attribute.Expr)
	}

	return true
}

func literalBool(expr hclsyntax.Expression) bool {
	value, diagnostics := expr.Value(nil)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsKnown() {
		return true
	}

	if value.Type().FriendlyName() != "bool" {
		return true
	}

	return value.True()
}

func (Generator) blockEdits(source []byte, where site, block *hclsyntax.Block) []edit {
	edits := blockRemovalEdits(source, where, block)

	if where.kind == resourceKind && !where.dynamic && where.address == where.resource {
		edits = append(edits, forEachToCountEdits(source, where, block)...)
	}

	return edits
}
