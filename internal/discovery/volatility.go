package discovery

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// impureFunctions return a different value on every evaluation.
//
// `uuidv5` is deliberately absent: it is a deterministic hash of its arguments,
// so masking it would erase a killable mutation (R2-9).
//
//nolint:gochecknoglobals // an immutable lookup table.
var impureFunctions = map[string]bool{
	"timestamp":      true,
	"plantimestamp":  true,
	"uuid":           true,
	"bcrypt":         true,
	"nonsensitive":   false,
	"uuidv5":         false,
	"base64sha256":   false,
	"filebase64sha1": false,
}

// volatileProviders generate values that differ per apply unless mocked.
//
//nolint:gochecknoglobals // an immutable lookup table.
var volatileProviders = []string{"random", "time"}

// VolatileAttribute is a statically-detected volatile value, decomposed into
// the literal components that survive masking.
//
// Decomposition is what makes the mask component-granular rather than
// scalar-granular (R2-9): `"${uuid()}-stable"` keeps its assertable suffix.
type VolatileAttribute struct {
	// Resource is the address of the resource the value belongs to.
	Resource string
	// Attribute is the top-level argument name inside that resource.
	Attribute string
	// Prefix and Suffix are the literal components of the template that
	// surround the impure part.
	Prefix string
	Suffix string
	// Whole marks a value with no literal component at all.
	Whole bool
}

// VolatilityScan is what a static pass over a module's syntax found.
type VolatilityScan struct {
	// Attributes are the decomposable impure values.
	Attributes []VolatileAttribute
	// Resources names every resource whose values the scan considers
	// suspicious, which is the granularity the mutant re-run rule needs.
	Resources map[string]bool
}

// Suspicious reports whether a resource address is one the scan flagged.
func (s VolatilityScan) Suspicious(address string) bool {
	return s.Resources[address]
}

// ScanVolatility finds the impure values in a set of module sources.
//
// Sources are supplied as bytes rather than read from disk so that the same
// scan can run over a mutant's rewritten file: the mutation-introduced
// volatility rule requires scanning the mutant's syntax, not the baseline's
// (M2 spec review, C4).
func (c Configuration) ScanVolatility(sources map[string][]byte) VolatilityScan {
	scan := VolatilityScan{Attributes: []VolatileAttribute{}, Resources: map[string]bool{}}
	mocked := c.mockedProviderNames()

	for path, content := range sources {
		file, diagnostics := hclsyntax.ParseConfig(content, path, hcl.InitialPos)
		if diagnostics.HasErrors() {
			continue
		}

		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}

		scanBody(body, content, mocked, &scan)
	}

	slices.SortFunc(scan.Attributes, func(left, right VolatileAttribute) int {
		if left.Resource != right.Resource {
			return strings.Compare(left.Resource, right.Resource)
		}

		return strings.Compare(left.Attribute, right.Attribute)
	})

	return scan
}

func (c Configuration) mockedProviderNames() map[string]bool {
	mocked := map[string]bool{}
	for _, name := range c.Tests.MockedProviders {
		mocked[name] = true
	}

	return mocked
}

func scanBody(body *hclsyntax.Body, source []byte, mocked map[string]bool, scan *VolatilityScan) {
	for _, block := range body.Blocks {
		if block.Type != resourceBlock && block.Type != dataBlock {
			continue
		}

		if len(block.Labels) != resourceLabelCount {
			continue
		}

		address := block.Labels[0] + "." + block.Labels[1]
		if block.Type == dataBlock {
			address = dataBlock + "." + address
		}

		if unmockedVolatileProvider(block.Labels[0], mocked) {
			scan.Resources[address] = true
		}

		scanResource(address, block, source, scan)
	}
}

// unmockedVolatileProvider reports a `random_*` or `time_*` resource whose
// provider no mock covers, which makes every value it produces volatile.
func unmockedVolatileProvider(resourceType string, mocked map[string]bool) bool {
	provider := ProviderOf(resourceType)

	return slices.Contains(volatileProviders, provider) && !mocked[provider]
}

func scanResource(address string, block *hclsyntax.Block, source []byte, scan *VolatilityScan) {
	for name, attribute := range block.Body.Attributes {
		if !containsImpureCall(attribute.Expr) {
			continue
		}

		scan.Resources[address] = true

		prefix, suffix, whole := decomposeTemplate(attribute.Expr, source)
		scan.Attributes = append(scan.Attributes, VolatileAttribute{
			Resource: address, Attribute: name, Prefix: prefix, Suffix: suffix, Whole: whole,
		})
	}

	for _, nested := range block.Body.Blocks {
		nestedScan := VolatilityScan{Attributes: []VolatileAttribute{}, Resources: scan.Resources}
		scanResource(address, nested, source, &nestedScan)
	}
}

func containsImpureCall(expr hclsyntax.Expression) bool {
	found := false

	_ = hclsyntax.VisitAll(expr, func(node hclsyntax.Node) hcl.Diagnostics {
		if call, ok := node.(*hclsyntax.FunctionCallExpr); ok && impureFunctions[call.Name] {
			found = true
		}

		return nil
	})

	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && impureFunctions[call.Name] {
		found = true
	}

	return found
}

// decomposeTemplate separates the literal components of a template whose
// interpolations are impure. A value with no literal component is wholly
// volatile, which is a sound decomposition rather than an undecidable one.
func decomposeTemplate(expr hclsyntax.Expression, source []byte) (prefix, suffix string, whole bool) {
	template, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok || len(template.Parts) == 0 {
		return "", "", true
	}

	prefix = literalPart(template.Parts[0])
	suffix = literalPart(template.Parts[len(template.Parts)-1])

	if len(template.Parts) == 1 {
		suffix = ""
	}

	_ = source

	if prefix == "" && suffix == "" {
		return "", "", true
	}

	return prefix, suffix, false
}

func literalPart(expr hclsyntax.Expression) string {
	literal, ok := expr.(*hclsyntax.LiteralValueExpr)
	if !ok || literal.Val.IsNull() || !literal.Val.IsKnown() {
		return ""
	}

	if literal.Val.Type().FriendlyName() != "string" {
		return ""
	}

	return literal.Val.AsString()
}
