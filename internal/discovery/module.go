package discovery

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// effectDataSources are data sources whose read executes an unsandboxed effect.
//
//nolint:gochecknoglobals // an immutable lookup table.
var effectDataSources = map[string]string{
	"external":               "data.external",
	"http":                   "data.http",
	"terraform_remote_state": "data.terraform_remote_state",
}

const (
	resourceBlock = "resource"
	dataBlock     = "data"
	outputBlock   = "output"
	localsBlock   = "locals"
	moduleBlock   = "module"
	variableBlock = "variable"

	resourceLabelCount = 2
	addressParts       = 2
)

func parseModule(parser *hclparse.Parser, current queued) (Module, error) {
	files, err := listFiles(current.dir, ".tf")
	if err != nil {
		return Module{}, err
	}

	module := Module{
		Dir:        current.dir,
		Files:      files,
		Bodies:     map[string]*hclsyntax.Body{},
		References: map[string][]Reference{},
	}

	providers := map[string]bool{}

	for _, path := range files {
		body, err := parseFile(parser, path)
		if err != nil {
			return Module{}, err
		}

		module.Bodies[path] = body

		if err := collectFile(&module, providers, path, body); err != nil {
			return Module{}, err
		}
	}

	module.Providers = sortedKeys(providers)

	return module, nil
}

func collectFile(module *Module, providers map[string]bool, path string, body *hclsyntax.Body) error {
	relative, err := filepath.Rel(module.Dir, path)
	if err != nil {
		return fmt.Errorf("resolving file path: %w", err)
	}

	relative = filepath.ToSlash(relative)
	localsIndex := 0

	for _, block := range body.Blocks {
		switch block.Type {
		case resourceBlock, dataBlock:
			if len(block.Labels) != resourceLabelCount {
				continue
			}

			discovered := newBlock(block.Type, path, relative, block)
			providers[ProviderOf(block.Labels[0])] = true

			collectEffects(module, discovered, block)

			if block.Type == resourceBlock {
				module.Resources = append(module.Resources, discovered)
			} else {
				module.DataSources = append(module.DataSources, discovered)
			}
		case outputBlock:
			if len(block.Labels) != 1 {
				continue
			}

			module.Outputs = append(module.Outputs, newBlock(outputBlock, path, relative, block))
		case localsBlock:
			module.Locals = append(module.Locals, localsFrom(path, relative, block, localsIndex)...)
			localsIndex++
		case moduleBlock:
			if len(block.Labels) != 1 {
				continue
			}

			module.Calls = append(module.Calls, newCall(module.Dir, path, block))
		case variableBlock:
			if len(block.Labels) != 1 {
				continue
			}

			module.Variables = append(module.Variables, newBlock(variableBlock, path, relative, block))
		case "provider":
			collectProviderBlock(module, block)
		case "terraform":
			collectRequiredProviders(providers, block)
		default:
		}
	}

	collectReferences(module, path, body)

	return nil
}

func newBlock(kind, path, relative string, block *hclsyntax.Block) Block {
	discovered := Block{
		Kind:       kind,
		File:       path,
		ModuleRel:  relative,
		DefRange:   block.DefRange(),
		Attributes: attributesOf(block.Body),
	}

	switch kind {
	case resourceBlock, dataBlock:
		discovered.Type = block.Labels[0]
		discovered.Name = block.Labels[1]
		discovered.Address = block.Labels[0] + "." + block.Labels[1]

		if kind == dataBlock {
			discovered.Address = dataBlock + "." + discovered.Address
		}
	default:
		discovered.Name = block.Labels[0]
		discovered.Address = kind + "." + block.Labels[0]
	}

	for _, attribute := range discovered.Attributes {
		switch attribute.Name {
		case "count":
			discovered.HasCount = true
		case "for_each":
			discovered.HasForEach = true
		default:
		}
	}

	return discovered
}

func localsFrom(path, relative string, block *hclsyntax.Block, index int) []Block {
	attributes := attributesOf(block.Body)
	locals := make([]Block, 0, len(attributes))

	for _, attribute := range attributes {
		locals = append(locals, Block{
			Kind:        "local",
			Name:        attribute.Name,
			Address:     "local." + attribute.Name,
			File:        path,
			ModuleRel:   relative,
			DefRange:    attribute.Range,
			Attributes:  []Attribute{attribute},
			LocalsIndex: index,
		})
	}

	return locals
}

func newCall(moduleDir, path string, block *hclsyntax.Block) ModuleCall {
	call := ModuleCall{
		Name:     block.Labels[0],
		File:     path,
		DefRange: block.DefRange(),
		Inputs:   []Attribute{},
	}

	for _, attribute := range attributesOf(block.Body) {
		if attribute.Name == "source" {
			call.Source = literalString(attribute.Expr)

			continue
		}

		if attribute.Name == "version" || attribute.Name == "providers" {
			continue
		}

		call.Inputs = append(call.Inputs, attribute)
	}

	call.Local = isLocalSource(call.Source)
	if call.Local {
		call.Dir = filepath.Clean(filepath.Join(moduleDir, call.Source))
	}

	return call
}

func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

func attributesOf(body *hclsyntax.Body) []Attribute {
	attributes := make([]Attribute, 0, len(body.Attributes))

	for name, attribute := range body.Attributes {
		attributes = append(attributes, Attribute{
			Name:  name,
			Range: attribute.SrcRange,
			Expr:  attribute.Expr,
		})
	}

	slices.SortFunc(attributes, func(left, right Attribute) int {
		return left.Range.Start.Byte - right.Range.Start.Byte
	})

	return attributes
}

func collectEffects(module *Module, discovered Block, block *hclsyntax.Block) {
	for _, nested := range block.Body.Blocks {
		if nested.Type == "provisioner" || nested.Type == "connection" {
			module.Effects = append(module.Effects, Effect{
				Kind:    "provisioner",
				Address: discovered.Address,
				File:    discovered.File,
				Range:   nested.DefRange(),
			})
		}
	}

	if discovered.Kind != dataBlock {
		return
	}

	if kind, found := effectDataSources[discovered.Type]; found {
		module.Effects = append(module.Effects, Effect{
			Kind:    kind,
			Address: discovered.Address,
			File:    discovered.File,
			Range:   discovered.DefRange,
		})
	}
}

func collectRequiredProviders(providers map[string]bool, block *hclsyntax.Block) {
	for _, nested := range block.Body.Blocks {
		if nested.Type != "required_providers" {
			continue
		}

		for name := range nested.Body.Attributes {
			providers[name] = true
		}
	}
}

// ProviderOf returns the provider local name implied by a resource type.
func ProviderOf(resourceType string) string {
	if strings.HasPrefix(resourceType, "terraform_") {
		return BuiltinProvider
	}

	name, _, found := strings.Cut(resourceType, "_")
	if !found {
		return resourceType
	}

	return name
}

func literalString(expr hclsyntax.Expression) string {
	value, diagnostics := expr.Value(nil)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsKnown() {
		return ""
	}

	if value.Type().FriendlyName() != "string" {
		return ""
	}

	return value.AsString()
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))

	for key := range set {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// collectReferences records every consumption of a resource address in the file.
func collectReferences(module *Module, path string, body *hclsyntax.Body) {
	walkExpressions(body, func(expr hclsyntax.Expression) {
		recordReference(module, path, expr)
	})
}

func recordReference(module *Module, path string, expr hclsyntax.Expression) {
	switch typed := expr.(type) {
	case *hclsyntax.SplatExpr:
		address, ok := resourceAddress(typed.Source)
		if ok {
			module.References[address] = append(module.References[address], Reference{
				Form:  ReferenceSplat,
				File:  path,
				Range: typed.Range(),
			})
		}
	case *hclsyntax.ScopeTraversalExpr:
		address, form, ok := classifyTraversal(typed.Traversal)
		if ok {
			module.References[address] = append(module.References[address], Reference{
				Form:  form,
				File:  path,
				Range: typed.Range(),
			})
		}
	case *hclsyntax.IndexExpr:
		address, ok := resourceAddress(typed.Collection)
		if ok {
			module.References[address] = append(module.References[address], Reference{
				Form:  ReferenceIndexed,
				File:  path,
				Range: typed.Range(),
			})
		}
	default:
	}
}

// resourceAddress returns the resource address a traversal expression names,
// when the traversal is exactly the resource collection.
func resourceAddress(expr hclsyntax.Expression) (string, bool) {
	traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok {
		return "", false
	}

	if len(traversal.Traversal) != addressParts {
		return "", false
	}

	return traversalAddress(traversal.Traversal)
}

func traversalAddress(traversal hcl.Traversal) (string, bool) {
	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", false
	}

	if isReservedRoot(root.Name) {
		return "", false
	}

	attribute, ok := traversal[1].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}

	return root.Name + "." + attribute.Name, true
}

func isReservedRoot(name string) bool {
	switch name {
	case "var", "local", "each", "count", "path", "terraform", "self", "output", "run", moduleBlock, dataBlock:
		return true
	default:
		return false
	}
}

// classifyTraversal reports the resource address a traversal consumes and how.
func classifyTraversal(traversal hcl.Traversal) (string, ReferenceForm, bool) {
	if len(traversal) < addressParts {
		return "", "", false
	}

	address, ok := traversalAddress(traversal)
	if !ok {
		return "", "", false
	}

	if len(traversal) == addressParts {
		return address, ReferenceWhole, true
	}

	switch traversal[addressParts].(type) {
	case hcl.TraverseIndex:
		return address, ReferenceIndexed, true
	default:
		return address, ReferenceBare, true
	}
}

// walkExpressions visits every expression in a body, including nested blocks.
func walkExpressions(body *hclsyntax.Body, visit func(hclsyntax.Expression)) {
	for _, attribute := range body.Attributes {
		walkExpression(attribute.Expr, visit)
	}

	for _, block := range body.Blocks {
		walkExpressions(block.Body, visit)
	}
}

func walkExpression(expr hclsyntax.Expression, visit func(hclsyntax.Expression)) {
	if expr == nil {
		return
	}

	visit(expr)

	// VisitAll only reports diagnostics the callback produces, and this one
	// produces none.
	_ = hclsyntax.VisitAll(expr, func(node hclsyntax.Node) hcl.Diagnostics {
		if nested, ok := node.(hclsyntax.Expression); ok && nested != expr {
			visit(nested)
		}

		return nil
	})
}
