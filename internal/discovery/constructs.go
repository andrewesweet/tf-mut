package discovery

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The constructs issue #70 found outside every inventory.
//
// A `check` block scopes a data source that Terraform evaluates during
// `terraform test`, and a `removed` block carries destroy-time provisioners
// and names the resource whose provider would run the destroy. Both were
// skipped by the native-syntax walker and left unread by the JSON reader, so
// their content reached Terraform and neither safety gate. `moved` and
// `import` stay uncollected — but refused by name rather than in silence,
// because silence is what kept `check` invisible for a milestone.

const (
	removedBlock  = "removed"
	movedBlock    = "moved"
	importBlock   = "import"
	fromAttribute = "from"
	lifecycleName = "lifecycle"
	kindLabel     = "kind"
)

// ErrUnmodelledConstruct reports a Terraform construct this version has no
// collector for. It is a refusal, not a parse failure: the configuration is
// valid and declares something the inventories cannot see, which is the case
// that must stop a run rather than proceed on a partial reading.
var ErrUnmodelledConstruct = errors.New("configuration declares a construct this version does not model")

// unmodelledConstruct reports a construct that is refused by name.
func unmodelledConstruct(kind, file string, position hcl.Pos) error {
	return fmt.Errorf("%w: %s block at %s:%d:%d", ErrUnmodelledConstruct,
		kind, file, position.Line, position.Column)
}

// collectCheckBlock walks a check block's scoped data source into the provider
// and effect inventories. The data source keeps Terraform's own address for it
// — `check.<name>.data.<type>.<name>` — so a refusal points at the construct
// the reader has to go and change.
func collectCheckBlock(
	module *Module,
	providers map[string]bool,
	path, relative string,
	block *hclsyntax.Block,
) {
	if len(block.Labels) != 1 {
		return
	}

	for _, nested := range block.Body.Blocks {
		if nested.Type != dataBlock || len(nested.Labels) != resourceLabelCount {
			continue
		}

		scoped := newBlock(dataBlock, path, relative, nested)
		scoped.Address = checkBlock + "." + block.Labels[0] + "." + scoped.Address

		providers[ProviderOf(nested.Labels[0])] = true

		collectEffects(module, scoped, nested)
	}
}

// collectRemovedBlock walks a removed block's destroy-time provisioners into
// the effect inventory, and the resource it names into the provider inventory:
// destroying a resource runs its provider, whether or not the configuration
// still declares the resource.
func collectRemovedBlock(
	module *Module,
	providers map[string]bool,
	path string,
	block *hclsyntax.Block,
) {
	address := removedBlock

	if attribute, found := block.Body.Attributes[fromAttribute]; found {
		if from, ok := resourceAddress(attribute.Expr); ok {
			address += "." + from
			providers[ProviderOf(resourceTypeOf(from))] = true
		}
	}

	for _, nested := range block.Body.Blocks {
		if nested.Type != provisionerBlock && nested.Type != connectionBlock {
			continue
		}

		module.Effects = append(module.Effects, Effect{
			Kind: provisionerBlock, Address: address, File: path, Range: nested.DefRange(),
		})
	}
}

// resourceTypeOf returns the resource type of a two-part resource address.
func resourceTypeOf(address string) string {
	resourceType, _, _ := strings.Cut(address, ".")

	return resourceType
}

// collectJSONCheck is collectCheckBlock's JSON-syntax twin. Reader symmetry is
// the point: a construct one syntax collects and the other leaves unread is
// exactly the shape that let a check-scoped effect through.
func collectJSONCheck(
	module *Module,
	providers map[string]bool,
	path string,
	block *hcl.Block,
) error {
	nested, rest, diagnostics := block.Body.PartialContent(jsonCheckSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if err := refuseUnmodelled(path, rest); err != nil {
		return err
	}

	for _, inner := range nested.Blocks {
		if inner.Type != dataBlock {
			if err := collectJSONReferences(module, path, inner.Body); err != nil {
				return err
			}

			continue
		}

		address := checkBlock + "." + block.Labels[0] + "." + dataBlock + "." +
			inner.Labels[0] + "." + inner.Labels[1]

		providers[ProviderOf(inner.Labels[0])] = true

		if kind, found := effectDataSources[inner.Labels[0]]; found {
			module.Effects = append(module.Effects, Effect{
				Kind: kind, Address: address, File: path, Range: inner.DefRange,
			})
		}

		if err := collectJSONReferences(module, path, inner.Body); err != nil {
			return err
		}
	}

	return nil
}

// collectJSONRemoved is collectRemovedBlock's JSON-syntax twin.
func collectJSONRemoved(
	module *Module,
	providers map[string]bool,
	path string,
	block *hcl.Block,
) error {
	nested, rest, diagnostics := block.Body.PartialContent(jsonRemovedSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if err := refuseUnmodelledExcept(path, rest, removedBlockAttributes); err != nil {
		return err
	}

	attributes, diagnostics := rest.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	address := removedBlock

	if attribute, found := attributes[fromAttribute]; found {
		if from, ok := jsonResourceAddress(attribute.Expr); ok {
			address += "." + from
			providers[ProviderOf(resourceTypeOf(from))] = true
		}
	}

	for _, inner := range nested.Blocks {
		if inner.Type != provisionerBlock && inner.Type != connectionBlock {
			continue
		}

		module.Effects = append(module.Effects, Effect{
			Kind: provisionerBlock, Address: address, File: path, Range: inner.DefRange,
		})
	}

	return nil
}

// jsonResourceAddress reads the resource address a JSON expression names.
func jsonResourceAddress(expr hcl.Expression) (string, bool) {
	for _, traversal := range expr.Variables() {
		if len(traversal) < addressParts {
			continue
		}

		if address, ok := traversalAddress(traversal); ok {
			return address, true
		}
	}

	return "", false
}
