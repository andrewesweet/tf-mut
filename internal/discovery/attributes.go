package discovery

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// AttributeRef is one attribute of one resource or data source type that the
// configuration reads downstream of the resource that produces it.
//
// It is what decides which mock values have to be pinned: an attribute nothing
// reads can be left to the mock generator, and an attribute something reads
// would otherwise vary between runs and make a generated assertion flaky by
// construction.
type AttributeRef struct {
	// Kind is resource or data.
	Kind string
	// Type is the resource or data source type.
	Type string
	// Attribute is the first path segment read off the resource.
	Attribute string
}

// ReferencedAttributes lists every resource attribute the closure's module
// expressions read, sorted and deduplicated.
func (c Configuration) ReferencedAttributes() []AttributeRef {
	seen := map[AttributeRef]bool{}

	for _, module := range c.Modules {
		for _, body := range module.Bodies {
			walkExpressions(body, func(expr hclsyntax.Expression) {
				for _, traversal := range expr.Variables() {
					if reference, ok := attributeRef(traversal); ok {
						seen[reference] = true
					}
				}
			})
		}

		for _, body := range module.JSONBodies {
			collectJSONAttributeRefs(body, seen)
		}
	}

	references := make([]AttributeRef, 0, len(seen))
	for reference := range seen {
		references = append(references, reference)
	}

	slices.SortFunc(references, func(left, right AttributeRef) int {
		if order := strings.Compare(left.Kind, right.Kind); order != 0 {
			return order
		}

		if order := strings.Compare(left.Type, right.Type); order != 0 {
			return order
		}

		return strings.Compare(left.Attribute, right.Attribute)
	})

	return references
}

// attributeRef reads a resource attribute out of a traversal, or reports that
// the traversal names something else.
func attributeRef(traversal hcl.Traversal) (AttributeRef, bool) {
	empty := AttributeRef{Kind: "", Type: "", Attribute: ""}

	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return empty, false
	}

	kind, offset := resourceBlock, 0
	if root.Name == dataBlock {
		kind, offset = dataBlock, 1
	} else if isReservedRoot(root.Name) {
		return empty, false
	}

	// type, name, attribute — the shortest traversal that names an attribute
	// of an addressed resource.
	const named = 3

	if len(traversal) < named+offset {
		return empty, false
	}

	resourceType := root.Name

	if offset == 1 {
		typed, isAttribute := traversal[1].(hcl.TraverseAttr)
		if !isAttribute {
			return empty, false
		}

		resourceType = typed.Name
	}

	attribute, ok := traversal[named+offset-1].(hcl.TraverseAttr)
	if !ok {
		return empty, false
	}

	return AttributeRef{Kind: kind, Type: resourceType, Attribute: attribute.Name}, true
}

// collectJSONAttributeRefs walks a JSON body's expressions for the same reads.
func collectJSONAttributeRefs(body hcl.Body, seen map[AttributeRef]bool) {
	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return
	}

	for _, attribute := range attributes {
		for _, traversal := range attribute.Expr.Variables() {
			if reference, ok := attributeRef(traversal); ok {
				seen[reference] = true
			}
		}
	}
}
