package discovery

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/hcl/v2/json"
)

// ErrUnmodelledJSON reports JSON content this version of the tool does not
// model. It is not a parse failure: the file is well-formed and declares
// something the reader has no schema for, which is exactly the case that must
// leave the safety floor down rather than lift it.
var ErrUnmodelledJSON = errors.New("JSON declares content this version does not model")

// The JSON slice is discover-only, by design and not by omission.
//
// Terraform's JSON variants express the same language, so everything the safety
// gates and the reference graph need can be read out of them. What cannot be
// read out of them is a mutation: Tier 0 rewrites through `hclwrite` and Tiers
// 1–3 rewrite byte ranges of native syntax, and neither has a JSON counterpart.
// A JSON file therefore contributes inventories, graph nodes and graph edges,
// and never a mutation site.

// jsonConfigurationSchema is the top-level block set of a `.tf.json` file.
//
// Anything outside it leaves the file unread. The list is deliberately the
// whole Terraform top-level vocabulary rather than only the parts the
// inventories consume: a block type missing from here is content the tool
// cannot see, and the floor exists for content the tool cannot see.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonConfigurationSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: terraformBlock, LabelNames: nil},
		{Type: providerBlock, LabelNames: []string{nameLabel}},
		{Type: resourceBlock, LabelNames: []string{typeLabel, nameLabel}},
		{Type: dataBlock, LabelNames: []string{typeLabel, nameLabel}},
		{Type: moduleBlock, LabelNames: []string{nameLabel}},
		{Type: outputBlock, LabelNames: []string{nameLabel}},
		{Type: localsBlock, LabelNames: nil},
		{Type: variableBlock, LabelNames: []string{nameLabel}},
		{Type: checkBlock, LabelNames: []string{nameLabel}},
		{Type: removedBlock, LabelNames: nil},
		// moved and import are listed so that they can be *refused by name*
		// (M4.5 spec review C4). Leaving them out would only leave the file
		// unread, and the floor that stands in for a reading is an opt-in away
		// from being lifted: grant --allow-real-infrastructure and
		// --allow-unsandboxed-effects and processing continues with neither
		// construct represented anywhere. A collector that refuses is not
		// overridable by an opt-in, which is what a construct this version
		// cannot model requires.
		{Type: movedBlock, LabelNames: nil},
		{Type: importBlock, LabelNames: nil},
	},
}

// jsonCheckSchema is the nested block set of a check block. Terraform permits
// at most one scoped data source per check; the schema accepts what the reader
// can walk and refuses the rest through the leftover body.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonCheckSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: dataBlock, LabelNames: []string{typeLabel, nameLabel}},
		{Type: assertBlock, LabelNames: nil},
	},
}

// jsonRemovedSchema is the nested block set of a removed block.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonRemovedSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: provisionerBlock, LabelNames: []string{kindLabel}},
		{Type: connectionBlock, LabelNames: nil},
		{Type: lifecycleName, LabelNames: nil},
	},
}

// removedBlockAttributes are the removed block arguments this version reads.
// `from` names the resource whose provider a destroy would run, and is the
// only argument the block takes.
//
//nolint:gochecknoglobals // an immutable allow-list.
var removedBlockAttributes = map[string]bool{fromAttribute: true}

// jsonResourceSchema is the nested block set of a resource or data block.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonResourceSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: provisionerBlock, LabelNames: []string{"kind"}},
		{Type: connectionBlock, LabelNames: nil},
		{Type: "lifecycle", LabelNames: nil},
		{Type: "dynamic", LabelNames: []string{nameLabel}},
	},
}

// jsonTerraformSchema is the nested block set of a `terraform` block.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonTerraformSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: requiredProviders, LabelNames: nil},
		{Type: "backend", LabelNames: []string{"type"}},
		{Type: "cloud", LabelNames: nil},
	},
}

// readJSONConfiguration decodes one `.tf.json` file into a module's
// inventories, and reports what stopped it where it could not.
func readJSONConfiguration(module *Module, providers map[string]bool, path string) error {
	body, err := parseJSONFile(path)
	if err != nil {
		return err
	}

	content, rest, diagnostics := body.PartialContent(jsonConfigurationSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if unmodelledErr := refuseUnmodelled(path, rest); unmodelledErr != nil {
		return unmodelledErr
	}

	relative, err := filepath.Rel(module.Dir, path)
	if err != nil {
		return fmt.Errorf("resolving file path: %w", err)
	}

	relative = filepath.ToSlash(relative)

	for _, block := range content.Blocks {
		if err := collectJSONBlock(module, providers, path, relative, block); err != nil {
			return err
		}
	}

	module.JSONBodies[path] = body

	return nil
}

func parseJSONFile(path string) (hcl.Body, error) {
	source, err := os.ReadFile(path) //nolint:gosec // module paths come from discovery.
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	file, diagnostics := json.Parse(source, path)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	return file.Body, nil
}

// refuseUnmodelled fails the read when the body carries anything the schema did
// not name. JSON draws no line between an attribute and a block, so whatever is
// left over is a construct this version has no reader for.
func refuseUnmodelled(path string, rest hcl.Body) error {
	return refuseUnmodelledExcept(path, rest, nil)
}

// refuseUnmodelledExcept is refuseUnmodelled with a deliberate allow-list of
// attributes the caller accepts without modelling — each one a recorded
// decision that the attribute cannot inform a safety gate.
func refuseUnmodelledExcept(path string, rest hcl.Body, allowed map[string]bool) error {
	attributes, diagnostics := rest.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	names := make([]string, 0, len(attributes))

	for name := range attributes {
		if !allowed[name] {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return nil
	}

	slices.Sort(names)

	return fmt.Errorf("%w: %s declares %s", ErrUnmodelledJSON, path, strings.Join(names, ", "))
}

func collectJSONBlock(
	module *Module,
	providers map[string]bool,
	path, relative string,
	block *hcl.Block,
) error {
	switch block.Type {
	case resourceBlock, dataBlock:
		return collectJSONResource(module, providers, path, relative, block)
	case terraformBlock:
		return collectJSONTerraform(providers, path, block)
	case providerBlock:
		return collectJSONProvider(module, path, block)
	case outputBlock, localsBlock:
		return collectJSONExpansion(module, path, block)
	case variableBlock:
		return collectJSONVariable(module, path, relative, block)
	case moduleBlock:
		return collectJSONModuleCall(module, path, block)
	case checkBlock:
		return collectJSONCheck(module, providers, path, block)
	case removedBlock:
		return collectJSONRemoved(module, providers, path, block)
	case movedBlock:
		// Accepted and collected into nothing: see constructs.go. The block is
		// still listed in the schema, so the file is read and its floor lifts
		// rather than standing in for a reading nobody made.
		return nil
	case importBlock:
		return unmodelledConstruct(block.Type, path, block.DefRange.Start)
	default:
		// Every remaining block type contributes references and graph nodes
		// through the body walk, and nothing to an inventory.
		return collectJSONReferences(module, path, block.Body)
	}
}

func collectJSONResource(
	module *Module,
	providers map[string]bool,
	path, relative string,
	block *hcl.Block,
) error {
	address := block.Labels[0] + "." + block.Labels[1]
	if block.Type == dataBlock {
		address = dataBlock + "." + address
	}

	discovered := Block{
		Kind: block.Type, Type: block.Labels[0], Name: block.Labels[1], Address: address,
		File: path, ModuleRel: relative, DefRange: block.DefRange,
		Attributes: nil, HasCount: false, HasForEach: false, LocalsIndex: 0,
	}

	providers[ProviderOf(block.Labels[0])] = true

	nested, rest, diagnostics := block.Body.PartialContent(jsonResourceSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	for _, inner := range nested.Blocks {
		if inner.Type == provisionerBlock || inner.Type == connectionBlock {
			module.Effects = append(module.Effects, Effect{
				Kind: provisionerBlock, Address: address, File: path, Range: inner.DefRange,
			})
		}

		if err := collectJSONReferences(module, path, inner.Body); err != nil {
			return err
		}
	}

	if kind, found := effectDataSources[discovered.Type]; found && discovered.Kind == dataBlock {
		module.Effects = append(module.Effects, Effect{
			Kind: kind, Address: address, File: path, Range: block.DefRange,
		})
	}

	attributes, diagnostics := rest.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	for name := range attributes {
		switch name {
		case countKeyword:
			discovered.HasCount = true
		case "for_each":
			discovered.HasForEach = true
		default:
		}
	}

	if block.Type == resourceBlock {
		module.JSONResources = append(module.JSONResources, discovered)
	} else {
		module.JSONDataSources = append(module.JSONDataSources, discovered)
	}

	return collectJSONReferences(module, path, rest)
}

// terraformBlockAttributes are the `terraform` block arguments this version
// deliberately accepts without modelling: none of them can declare a provider,
// an effect or a run, so none can inform a safety gate. Anything else in the
// block is content this version has no reader for, and refusing it is what
// keeps a future Terraform construct from becoming silent permission.
//
//nolint:gochecknoglobals // an immutable allow-list.
var terraformBlockAttributes = map[string]bool{
	"required_version": true,
}

func collectJSONTerraform(providers map[string]bool, path string, block *hcl.Block) error {
	nested, rest, diagnostics := block.Body.PartialContent(jsonTerraformSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if err := refuseUnmodelledExcept(path, rest, terraformBlockAttributes); err != nil {
		return err
	}

	for _, inner := range nested.Blocks {
		if inner.Type != requiredProviders {
			continue
		}

		attributes, attributeDiagnostics := inner.Body.JustAttributes()
		if attributeDiagnostics.HasErrors() {
			return fmt.Errorf("%w: %s: %s", ErrParse, path, attributeDiagnostics.Error())
		}

		for name := range attributes {
			providers[name] = true
		}
	}

	return nil
}

func collectJSONProvider(module *Module, path string, block *hcl.Block) error {
	declared := ProviderAlias{Name: block.Labels[0], Alias: ""}

	attributes, diagnostics := block.Body.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if attribute, found := attributes["alias"]; found {
		declared.Alias = jsonLiteralString(attribute.Expr)
	}

	module.ProviderAliases = append(module.ProviderAliases, declared)

	return collectJSONReferences(module, path, block.Body)
}

// collectJSONModuleCall decodes a JSON-declared module call into the closure.
//
// The call must enter `module.Calls`: discovery follows the local-module
// closure exclusively through it, and a call that is graphed but never queued
// leaves the child's providers and effects outside both safety gates — the
// exact fail-open edge the floor exists to close. Inputs are deliberately not
// decoded (no native-syntax expression exists for them), which is why the
// reference graph treats a JSON-declared call as unbounded.
func collectJSONModuleCall(module *Module, path string, block *hcl.Block) error {
	attributes, diagnostics := block.Body.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	source, found := attributes["source"]
	if !found {
		return fmt.Errorf("%w: %s declares module %q with no source", ErrParse, path, block.Labels[0])
	}

	call := ModuleCall{
		Name: block.Labels[0], Source: jsonLiteralString(source.Expr),
		Local: false, Dir: "", File: path, DefRange: block.DefRange,
		Inputs: nil, JSONDeclared: true,
	}

	if call.Source == "" {
		return fmt.Errorf("%w: %s declares module %q whose source is not a constant string",
			ErrUnmodelledJSON, path, block.Labels[0])
	}

	call.Local = isLocalSource(call.Source)
	if call.Local {
		call.Dir = filepath.Clean(filepath.Join(module.Dir, call.Source))
	}

	module.Calls = append(module.Calls, call)

	return collectJSONReferences(module, path, block.Body)
}

// collectJSONExpansion records what a JSON-declared output or locals block
// observes, so the assertion closure can follow a delta through it.
// jsonVariableSchema is a variable declaration's body in JSON.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonVariableSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: typeLabel, Required: false},
		{Name: "default", Required: false},
		{Name: "description", Required: false},
		{Name: "sensitive", Required: false},
		{Name: "ephemeral", Required: false},
		{Name: "nullable", Required: false},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: validationBlock, LabelNames: nil},
	},
}

// collectJSONVariable puts a JSON-declared input into the same inventory a
// native one lands in.
//
// Terraform reads `.tf.json` variables exactly as it reads native ones, so a
// collector that only walked their references left `Module.Variables` empty for
// a JSON module: characterisation then synthesised no assignment and raised no
// judgement point, and the run died at plan time on "No value for required
// variable" — a module the tool had read, and said nothing about.
//
// The conversion is a re-parse, because the rest of the tool reads native
// syntax trees. A JSON type constraint is a *string* holding native type
// syntax ("list(string)"), and a JSON validation condition is a string holding
// a template around one. Anything that does not re-parse is left off the block
// rather than guessed at, and the variable then reaches the reader as a
// judgement point — the fail-closed direction, and the one the design already
// reserves for a value the tool will not invent.
func collectJSONVariable(module *Module, path, relative string, block *hcl.Block) error {
	if len(block.Labels) != 1 {
		return nil
	}

	content, _, diagnostics := block.Body.PartialContent(jsonVariableSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	discovered := Block{ //nolint:exhaustruct // a variable carries no type, address parts or meta-arguments.
		Kind:      variableBlock,
		Name:      block.Labels[0],
		Address:   variableBlock + "." + block.Labels[0],
		File:      path,
		ModuleRel: relative,
		DefRange:  block.DefRange,
	}

	for name, attribute := range content.Attributes {
		expr, ok := nativeExpression(name, attribute.Expr)
		if !ok {
			continue
		}

		discovered.Attributes = append(discovered.Attributes, Attribute{
			Name: name, Range: attribute.Range, Expr: expr,
		})
	}

	slices.SortFunc(discovered.Attributes, func(left, right Attribute) int {
		return strings.Compare(left.Name, right.Name)
	})

	discovered.Validations = jsonValidations(path, content.Blocks)
	module.Variables = append(module.Variables, discovered)

	return collectJSONReferences(module, path, block.Body)
}

// nativeExpression re-parses one JSON-declared variable argument as native
// syntax, which is what every reader downstream of discovery expects.
//
// `type` is the special case: Terraform spells a JSON type constraint as a
// string containing native type syntax, so the string's *contents* are the
// expression. Every other argument is an ordinary JSON literal, and JSON
// literal syntax is a subset of HCL expression syntax, so its own source text
// parses unchanged.
func nativeExpression(name string, expr hcl.Expression) (hclsyntax.Expression, bool) {
	var source string

	if name == typeLabel {
		source = jsonLiteralString(expr)
	} else {
		value, diagnostics := expr.Value(nil)
		if diagnostics.HasErrors() || !value.IsWhollyKnown() {
			return nil, false
		}

		source = strings.TrimSpace(string(hclwrite.TokensForValue(value).Bytes()))
	}

	if source == "" {
		return nil, false
	}

	return parseNative(source, expr.Range())
}

// jsonValidations converts a JSON variable's validation blocks, in declaration
// order, dropping any whose condition does not re-parse.
func jsonValidations(path string, blocks hcl.Blocks) []Validation {
	validations := []Validation{}

	for _, nested := range blocks {
		if nested.Type != validationBlock {
			continue
		}

		attributes, diagnostics := nested.Body.JustAttributes()
		if diagnostics.HasErrors() {
			continue
		}

		condition, found := attributes["condition"]
		if !found {
			continue
		}

		// A JSON condition is a template around the expression — Terraform's
		// own `"${var.x != null}"` spelling — so the interpolation markers come
		// off before the contents are parsed as an expression.
		expr, ok := parseNative(unwrapInterpolation(jsonSource(path, condition.Expr)),
			condition.Expr.Range())
		if !ok {
			continue
		}

		validations = append(validations, Validation{
			Condition: expr, File: path, Range: condition.Expr.Range(),
		})
	}

	return validations
}

// jsonSource recovers a JSON string attribute's contents without evaluating
// it, which a condition reading `var.x` could never survive.
func jsonSource(path string, expr hcl.Expression) string {
	source, err := os.ReadFile(path) //nolint:gosec // module paths come from discovery.
	if err != nil {
		return ""
	}

	span := expr.Range()
	if span.Start.Byte < 0 || span.End.Byte > len(source) || span.Start.Byte >= span.End.Byte {
		return ""
	}

	quoted := string(source[span.Start.Byte:span.End.Byte])

	unquoted := ""
	if stdjson.Unmarshal([]byte(quoted), &unquoted) != nil {
		return ""
	}

	return unquoted
}

// unwrapInterpolation strips a template that wraps one expression and nothing
// else. A condition spelled any other way is left alone and will not parse,
// which is the outcome an unmodelled spelling should have.
func unwrapInterpolation(source string) string {
	trimmed := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmed, "${") || !strings.HasSuffix(trimmed, "}") {
		return trimmed
	}

	inner := trimmed[len("${") : len(trimmed)-1]
	if strings.Contains(inner, "${") {
		return trimmed
	}

	return inner
}

// parseNative parses native expression syntax at the JSON source's position, so
// that a diagnostic still points at the file the reader is looking at.
func parseNative(source string, span hcl.Range) (hclsyntax.Expression, bool) {
	expr, diagnostics := hclsyntax.ParseExpression([]byte(source), span.Filename, span.Start)
	if diagnostics.HasErrors() {
		return nil, false
	}

	return expr, true
}

func collectJSONExpansion(module *Module, path string, block *hcl.Block) error {
	attributes, diagnostics := block.Body.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	for name, attribute := range attributes {
		address := "local." + name
		if block.Type == outputBlock {
			if name != "value" {
				continue
			}

			address = outputBlock + "." + block.Labels[0]
		}

		module.JSONExpansions[address] = append(module.JSONExpansions[address],
			jsonRefs(attribute.Expr)...)
	}

	return collectJSONReferences(module, path, block.Body)
}

// collectJSONReferences records every resource consumption in a JSON body,
// which is what the multiplicity gate reads.
func collectJSONReferences(module *Module, path string, body hcl.Body) error {
	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	for _, attribute := range attributes {
		for _, traversal := range attribute.Expr.Variables() {
			address, form, ok := classifyTraversal(traversal)
			if !ok {
				continue
			}

			module.References[address] = append(module.References[address], Reference{
				Form: form, File: path, Range: attribute.Range,
			})
		}
	}

	return nil
}

// jsonRefs converts a JSON expression's traversals into closure references.
//
// Every one is imprecise. `hcl.Expression.Variables` reports which addresses an
// expression observes and nothing about how: a splat, a `for` projection and a
// direct read are indistinguishable through it. Precision is a claim that
// reading the expression proves the address's own value was read, and this
// reader cannot make it — so the closure degrades a JSON-mediated read to
// `unasserted` rather than calling it a weak assertion it cannot prove.
func jsonRefs(expr hcl.Expression) []Ref {
	refs := []Ref{}

	for _, traversal := range expr.Variables() {
		address, ok := traversalRef(traversal)
		if !ok {
			continue
		}

		refs = append(refs, Ref{
			Address: address, Precise: false, Construct: "JSON-declared expression",
		})
	}

	slices.SortFunc(refs, func(left, right Ref) int {
		return strings.Compare(left.Address, right.Address)
	})

	return refs
}

// jsonLiteralString renders a constant JSON string, or empty where the
// expression is not one.
func jsonLiteralString(expr hcl.Expression) string {
	value, diagnostics := expr.Value(nil)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsKnown() {
		return ""
	}

	if value.Type().FriendlyName() != "string" {
		return ""
	}

	return value.AsString()
}
