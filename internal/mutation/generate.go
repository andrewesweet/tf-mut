package mutation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// metaArguments are never deleted by the extreme operators: they are not
// provider schema arguments, and mutating them belongs to later tiers.
//
//nolint:gochecknoglobals // an immutable lookup table.
var metaArguments = map[string]bool{
	"count":      true,
	"for_each":   true,
	"depends_on": true,
	"provider":   true,
	"providers":  true,
	"lifecycle":  true,
	"source":     true,
	"version":    true,
}

// The block kinds the generator distinguishes.
const (
	resourceKind = "resource"
	dataKind     = "data"
)

// errUnknownSource reports a mutation site in a file discovery never read.
var errUnknownSource = errors.New("mutation site is in an unknown source file")

// errRewrite reports a file hclwrite could not re-parse for editing.
var errRewrite = errors.New("mutation site could not be rewritten")

// Selection is the operator population the run asked for.
type Selection struct {
	// Tier is the breadth band; operators above it are not generated.
	Tier Tier
	// Include, when non-empty, restricts generation to these operator
	// identifiers regardless of tier.
	Include []string
	// Exclude removes operator identifiers from the population.
	Exclude []string
}

// Enabled reports whether an operator is in the selected population.
func (s Selection) Enabled(operator Operator) bool {
	if slices.Contains(s.Exclude, string(operator)) {
		return false
	}

	if len(s.Include) > 0 {
		return slices.Contains(s.Include, string(operator))
	}

	tier := s.Tier
	if !tier.Valid() {
		tier = TierStandard
	}

	return tier.Includes(TierOf(operator))
}

// Generator produces the mutant population.
type Generator struct {
	// Configuration is the discovered module under test.
	Configuration discovery.Configuration
	// Schemas gates the deletion operators on per-attribute optionality.
	Schemas tfexec.Schemas
	// Selection is the operator population the run asked for.
	Selection Selection
}

// Result is the generated population and what generation learned about itself.
type Result struct {
	Mutants  []Mutant
	Warnings []string
}

// Generate returns the deduplicated mutant population in deterministic order.
func (g Generator) Generate() (Result, error) {
	sources, err := g.loadSources()
	if err != nil {
		return Result{}, err
	}

	mutants := []Mutant{}
	warnings := []string{}

	for _, module := range g.Configuration.Modules {
		produced, err := g.generateModule(module, sources)
		if err != nil {
			return Result{}, err
		}

		mutants = append(mutants, produced...)

		syntactic, rejected := g.generateSyntax(module, sources)
		mutants = append(mutants, syntactic...)
		warnings = append(warnings, rejected...)
	}

	enabled := make([]Mutant, 0, len(mutants))

	for _, mutant := range mutants {
		if g.Selection.Enabled(mutant.Operator) {
			enabled = append(enabled, mutant)
		}
	}

	return Result{Mutants: deduplicate(sortMutants(enabled)), Warnings: warnings}, nil
}

type sourceFile struct {
	content []byte
	rel     string
}

func (g Generator) loadSources() (map[string]sourceFile, error) {
	sources := map[string]sourceFile{}

	for _, module := range g.Configuration.Modules {
		for _, path := range module.Files {
			content, err := os.ReadFile(path) //nolint:gosec // module paths come from discovery.
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", path, err)
			}

			rel, err := filepath.Rel(g.Configuration.ClosureRoot, path)
			if err != nil {
				return nil, fmt.Errorf("resolving %s: %w", path, err)
			}

			sources[path] = sourceFile{content: content, rel: filepath.ToSlash(rel)}
		}
	}

	return sources, nil
}

func (g Generator) generateModule(module discovery.Module, sources map[string]sourceFile) ([]Mutant, error) {
	mutants := []Mutant{}

	for _, output := range module.Outputs {
		where := anchor{anchor: output, operator: OutputNull, address: output.Address, resource: ""}

		mutant, err := g.build(module, sources, where, func(file *hclwrite.File) bool {
			block := findBlock(file, "output", output.Name)
			if block == nil || block.Body().GetAttribute("value") == nil {
				return false
			}

			block.Body().SetAttributeValue("value", cty.NullVal(cty.DynamicPseudoType))

			return true
		})
		if err != nil {
			return nil, err
		}

		mutants = appendMutant(mutants, mutant)
	}

	for _, local := range module.Locals {
		where := anchor{anchor: local, operator: LocalNull, address: local.Address, resource: ""}

		mutant, err := g.build(module, sources, where, func(file *hclwrite.File) bool {
			block := findNthBlock(file, "locals", local.LocalsIndex)
			if block == nil || block.Body().GetAttribute(local.Name) == nil {
				return false
			}

			block.Body().SetAttributeValue(local.Name, cty.NullVal(cty.DynamicPseudoType))

			return true
		})
		if err != nil {
			return nil, err
		}

		mutants = appendMutant(mutants, mutant)
	}

	resourceMutants, err := g.generateResources(module, sources)
	if err != nil {
		return nil, err
	}

	mutants = append(mutants, resourceMutants...)

	callMutants, err := g.generateCalls(module, sources)
	if err != nil {
		return nil, err
	}

	return append(mutants, callMutants...), nil
}

func (g Generator) generateResources(module discovery.Module, sources map[string]sourceFile) ([]Mutant, error) {
	mutants := []Mutant{}

	blocks := append(append([]discovery.Block{}, module.Resources...), module.DataSources...)

	for _, block := range blocks {
		attributeMutants, err := g.generateAttributeDeletes(module, sources, block)
		if err != nil {
			return nil, err
		}

		mutants = append(mutants, attributeMutants...)

		if block.Kind != resourceKind {
			continue
		}

		instanceMutant, err := g.generateInstanceMutant(module, sources, block)
		if err != nil {
			return nil, err
		}

		mutants = appendMutant(mutants, instanceMutant)
	}

	return mutants, nil
}

func (g Generator) generateAttributeDeletes(
	module discovery.Module,
	sources map[string]sourceFile,
	block discovery.Block,
) ([]Mutant, error) {
	mutants := []Mutant{}

	for _, attribute := range g.optionalArguments(block) {
		name := attribute.Name
		where := anchor{
			anchor:   block,
			operator: AttrDelete,
			address:  block.Address + "." + name,
			resource: managedResource(block),
		}

		mutant, err := g.build(module, sources, where, func(file *hclwrite.File) bool {
			target := findBlock(file, block.Kind, blockLabels(block)...)
			if target == nil || target.Body().GetAttribute(name) == nil {
				return false
			}

			return target.Body().RemoveAttribute(name) != nil
		})
		if err != nil {
			return nil, err
		}

		if mutant != nil {
			mutant.Range = attribute.Range
		}

		mutants = appendMutant(mutants, mutant)
	}

	return mutants, nil
}

// optionalArguments returns the block's schema-optional argument assignments.
func (g Generator) optionalArguments(block discovery.Block) []discovery.Attribute {
	optional := []discovery.Attribute{}

	for _, attribute := range block.Attributes {
		if metaArguments[attribute.Name] {
			continue
		}

		isOptional, known := g.Schemas.Optionality(block.Kind, block.Type, attribute.Name)
		if !known || !isOptional {
			continue
		}

		optional = append(optional, attribute)
	}

	return optional
}

// generateInstanceMutant applies the normative multiplicity table: an existing
// count or for_each may be emptied only when every consumer tolerates an empty
// collection; every other resource gets EXT-BODY-BLANK instead. A count meta
// argument is never added, because a bare consumer makes that statically
// invalid and a for_each resource rejects it outright.
func (g Generator) generateInstanceMutant(
	module discovery.Module,
	sources map[string]sourceFile,
	block discovery.Block,
) (*Mutant, error) {
	if (block.HasCount || block.HasForEach) && g.consumersTolerateEmpty(module, block.Address) {
		meta := "count"
		value := cty.NumberIntVal(0)

		if block.HasForEach {
			meta = "for_each"
			value = cty.MapValEmpty(cty.String)
		}

		where := anchor{
			anchor:   block,
			operator: ResourceDelete,
			address:  block.Address,
			resource: managedResource(block),
		}

		return g.build(module, sources, where, func(file *hclwrite.File) bool {
			target := findBlock(file, block.Kind, blockLabels(block)...)
			if target == nil || target.Body().GetAttribute(meta) == nil {
				return false
			}

			target.Body().SetAttributeValue(meta, value)

			return true
		})
	}

	names := []string{}
	for _, attribute := range g.optionalArguments(block) {
		names = append(names, attribute.Name)
	}

	if len(names) == 0 {
		return nil, nil //nolint:nilnil // no body to blank is a legitimate absence of a mutant.
	}

	where := anchor{
		anchor:   block,
		operator: BodyBlank,
		address:  block.Address,
		resource: managedResource(block),
	}

	return g.build(module, sources, where, func(file *hclwrite.File) bool {
		target := findBlock(file, block.Kind, blockLabels(block)...)
		if target == nil {
			return false
		}

		removed := false

		for _, name := range names {
			if target.Body().RemoveAttribute(name) != nil {
				removed = true
			}
		}

		return removed
	})
}

// consumersTolerateEmpty reports whether every recorded consumption of the
// address survives an empty instance set, across the whole closure and the
// test files.
func (g Generator) consumersTolerateEmpty(owner discovery.Module, address string) bool {
	for _, reference := range owner.References[address] {
		if !reference.Form.Tolerates() {
			return false
		}
	}

	for _, reference := range g.Configuration.Tests.References[address] {
		if !reference.Form.Tolerates() {
			return false
		}
	}

	return true
}

func (g Generator) generateCalls(module discovery.Module, sources map[string]sourceFile) ([]Mutant, error) {
	mutants := []Mutant{}

	for _, call := range module.Calls {
		if !call.Local {
			continue
		}

		child, found := g.Configuration.ModuleByDir(call.Dir)
		if !found {
			continue
		}

		for _, input := range call.Inputs {
			name := input.Name

			// Inputs the child does not declare cannot be deleted meaningfully.
			// Inputs for a required variable are kept: deleting one is exactly
			// the statically invalid mutant the classifier must discard, and
			// discarding it is a promised behaviour rather than wasted work.
			if _, declared := child.VariableByName(name); !declared {
				continue
			}

			block := discovery.Block{
				Kind:      "module",
				Name:      call.Name,
				Address:   "module." + call.Name,
				File:      call.File,
				ModuleRel: filepath.Base(call.File),
				DefRange:  call.DefRange,
			}

			where := anchor{
				anchor:   block,
				operator: ModuleInputDelete,
				address:  block.Address + "." + name,
				resource: "",
			}

			mutant, err := g.build(module, sources, where, func(file *hclwrite.File) bool {
				target := findBlock(file, "module", call.Name)
				if target == nil || target.Body().GetAttribute(name) == nil {
					return false
				}

				return target.Body().RemoveAttribute(name) != nil
			})
			if err != nil {
				return nil, err
			}

			if mutant != nil {
				mutant.Range = input.Range
			}

			mutants = appendMutant(mutants, mutant)
		}
	}

	return mutants, nil
}

// anchor is everything a block-level mutant needs to describe itself: which
// block it fires on, under which operator, at which address.
type anchor struct {
	// anchor is the block whose file is rewritten and whose range is reported.
	anchor discovery.Block
	// operator is the operator producing the mutant.
	operator Operator
	// address is the semantic address of the mutation site.
	address string
	// resource is the managed resource the site belongs to, when it has one.
	resource string
}

// build applies one rewrite to the file declaring the site's anchor block.
func (Generator) build(
	module discovery.Module,
	sources map[string]sourceFile,
	where anchor,
	rewrite func(*hclwrite.File) bool,
) (*Mutant, error) {
	anchor := where.anchor

	source, found := sources[anchor.File]
	if !found {
		return nil, fmt.Errorf("%w: %s", errUnknownSource, anchor.File)
	}

	file, diagnostics := hclwrite.ParseConfig(source.content, anchor.File, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", errRewrite, anchor.File, diagnostics.Error())
	}

	if !rewrite(file) {
		return nil, nil //nolint:nilnil // the operator did not apply at this site.
	}

	mutated := file.Bytes()

	return &Mutant{
		ID: identify(where.operator, source.rel, where.address,
			removedLines(source.content, mutated), addedLines(source.content, mutated)),
		Operator:  where.operator,
		ModuleRel: module.Rel,
		File:      source.rel,
		Site:      where.address,
		Resource:  where.resource,
		Range:     anchor.DefRange,
		Diff:      UnifiedDiff(source.rel, source.content, mutated),
		Mutated:   mutated,
	}, nil
}

func appendMutant(mutants []Mutant, mutant *Mutant) []Mutant {
	if mutant == nil {
		return mutants
	}

	return append(mutants, *mutant)
}

// managedResource is the resource address a site belongs to. Data sources are
// deliberately excluded: the pseudo-tested headline is about managed resources,
// and a data source is not one.
func managedResource(block discovery.Block) string {
	if block.Kind != resourceKind {
		return ""
	}

	return block.Address
}

func blockLabels(block discovery.Block) []string {
	if block.Kind == resourceKind || block.Kind == dataKind {
		return []string{block.Type, block.Name}
	}

	return []string{block.Name}
}

func findBlock(file *hclwrite.File, blockType string, labels ...string) *hclwrite.Block {
	for _, block := range file.Body().Blocks() {
		if block.Type() != blockType {
			continue
		}

		if labelsEqual(block.Labels(), labels) {
			return block
		}
	}

	return nil
}

func findNthBlock(file *hclwrite.File, blockType string, index int) *hclwrite.Block {
	seen := 0

	for _, block := range file.Body().Blocks() {
		if block.Type() != blockType {
			continue
		}

		if seen == index {
			return block
		}

		seen++
	}

	return nil
}

func labelsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

func sortMutants(mutants []Mutant) []Mutant {
	slices.SortStableFunc(mutants, func(left, right Mutant) int {
		if left.File != right.File {
			return strings.Compare(left.File, right.File)
		}

		if left.Range.Start.Byte != right.Range.Start.Byte {
			return left.Range.Start.Byte - right.Range.Start.Byte
		}

		if left.Operator != right.Operator {
			return strings.Compare(string(left.Operator), string(right.Operator))
		}

		return strings.Compare(left.Site, right.Site)
	})

	return mutants
}

// deduplicate drops mutants that rewrite the same file to the same content.
func deduplicate(mutants []Mutant) []Mutant {
	seen := map[string]bool{}
	unique := []Mutant{}

	for _, mutant := range mutants {
		digest := sha256.Sum256(mutant.Mutated)
		key := mutant.File + ":" + string(digest[:])

		if seen[key] {
			continue
		}

		seen[key] = true

		unique = append(unique, mutant)
	}

	return unique
}
