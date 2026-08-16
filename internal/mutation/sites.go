package mutation

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// site is one attribute assignment in the context that gives it meaning.
//
// The context is what lets an operator decide whether it may fire at all: a
// literal `0` means something different as a `count`, as a variable default and
// as a CIDR prefix length, and only the address distinguishes them.
type site struct {
	// address is the semantic address of the attribute.
	address string
	// resource is the managed resource address the attribute belongs to.
	resource string
	// kind is the enclosing block kind: resource, data, module, output, local,
	// variable, check or locals.
	kind string
	// blockType is the resource or data source type, for schema lookups.
	blockType string
	// variable is the enclosing variable's name, which the validation operators
	// need in order to emit a condition that still refers to it.
	variable string
	// attributeName is the attribute's own name inside its block.
	attributeName string
	// contract marks an attribute inside a validation, pre/postcondition or
	// check assertion, which Tier 3 owns.
	contract bool
	// lifecycle marks an attribute inside a lifecycle block. Tier 4 owns those,
	// and this milestone does not ship it.
	lifecycle bool
	// dynamic marks an attribute inside a dynamic block.
	dynamic bool
}

// The block kinds walkBlocks distinguishes.
const (
	outputKind    = "output"
	localKind     = "local"
	moduleKind    = "module"
	variableKind  = "variable"
	checkKind     = "check"
	terraformKind = "terraform"
)

// visitor receives every attribute of a module file in context.
type visitor func(where site, attribute *hclsyntax.Attribute)

// blockVisitor receives every nested block of a module file in context.
type blockVisitor func(where site, block *hclsyntax.Block)

// walkModuleFile visits every attribute and nested block of a module file.
//
// The `terraform` block is skipped entirely: its contents are the tool's own
// contract with Terraform, and mutating a required provider version tests the
// registry rather than the module.
func walkModuleFile(body *hclsyntax.Body, attributes visitor, blocks blockVisitor) {
	for _, block := range body.Blocks {
		where, ok := contextOf(block)
		if !ok {
			continue
		}

		walkBlock(where, block, attributes, blocks)
	}
}

func contextOf(block *hclsyntax.Block) (site, bool) {
	base := site{
		address: "", resource: "", kind: block.Type,
		blockType: "", variable: "", attributeName: "",
		contract: false, lifecycle: false, dynamic: false,
	}

	switch block.Type {
	case resourceKind, dataKind:
		if len(block.Labels) != resourceLabels {
			return site{}, false
		}

		address := block.Labels[0] + "." + block.Labels[1]
		if block.Type == dataKind {
			address = dataKind + "." + address
		}

		base.address = address
		base.blockType = block.Labels[0]

		if block.Type == resourceKind {
			base.resource = address
		}
	case outputKind, moduleKind, variableKind, checkKind:
		if len(block.Labels) != 1 {
			return site{}, false
		}

		base.address = block.Type + "." + block.Labels[0]

		if block.Type == moduleKind {
			base.address = moduleKind + "." + block.Labels[0]
		}

		if block.Type == variableKind {
			base.variable = block.Labels[0]
			base.address = "var." + block.Labels[0]
		}
	case "locals":
		base.kind = localKind
		base.address = ""
	default:
		return site{}, false
	}

	return base, true
}

// resourceLabels is the label count of a resource or data block.
const resourceLabels = 2

func walkBlock(where site, block *hclsyntax.Block, attributes visitor, blocks blockVisitor) {
	blocks(where, block)

	for name, attribute := range block.Body.Attributes {
		attributeSite := where
		attributeSite.attributeName = name

		if where.kind == localKind {
			attributeSite.address = "local." + name
		} else {
			attributeSite.address = where.address + "." + name
		}

		attributes(attributeSite, attribute)
	}

	for _, nested := range block.Body.Blocks {
		nestedSite := where
		nestedSite.address = where.address + "." + nested.Type

		if len(nested.Labels) > 0 {
			nestedSite.address += "." + nested.Labels[0]
		}

		nestedSite.kind = where.kind
		nestedSite.contract = where.contract || isContractBlock(nested.Type)
		nestedSite.lifecycle = where.lifecycle || nested.Type == "lifecycle"
		nestedSite.dynamic = where.dynamic || nested.Type == "dynamic"

		walkBlock(nestedSite, nested, attributes, blocks)
	}
}

// walkExpressionTree visits an expression and every expression under it.
func walkExpressionTree(root hclsyntax.Expression, visit func(hclsyntax.Expression)) {
	if root == nil {
		return
	}

	visit(root)

	// VisitAll only surfaces diagnostics the callback returns, and this one
	// returns none.
	_ = hclsyntax.VisitAll(root, func(node hclsyntax.Node) hcl.Diagnostics {
		if nested, ok := node.(hclsyntax.Expression); ok && nested != root {
			visit(nested)
		}

		return nil
	})
}
