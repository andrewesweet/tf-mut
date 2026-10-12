package tfexec

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// dataSourceKind selects the data source half of a provider schema.
const dataSourceKind = "data"

// Schemas is the decoded output of terraform providers schema -json.
type Schemas struct {
	FormatVersion   string                    `json:"format_version"`
	ProviderSchemas map[string]ProviderSchema `json:"provider_schemas"`
	// Usage is the invocation's child-process accounting, not part of the
	// decoded document.
	Usage   Usage `json:"-"`
	Started bool  `json:"-"`
}

// ProviderSchema holds one provider's resource and data source schemas.
type ProviderSchema struct {
	ResourceSchemas   map[string]Schema `json:"resource_schemas"`
	DataSourceSchemas map[string]Schema `json:"data_source_schemas"`
}

// Schema is the schema of a single resource or data source.
type Schema struct {
	Block SchemaBlock `json:"block"`
}

// SchemaBlock is a configuration block schema.
type SchemaBlock struct {
	Attributes map[string]SchemaAttribute `json:"attributes,omitempty"`
	// BlockTypes holds the schema's nested configuration blocks. Most
	// providers — hashicorp/null among them — publish none, and type every
	// nested structure as a list or map of object instead; both shapes are
	// first-class to the consumers of this schema.
	BlockTypes map[string]SchemaBlockType `json:"block_types,omitempty"`
}

// SchemaBlockType is the schema of one nested configuration block.
type SchemaBlockType struct {
	// NestingMode is the block's nesting mode as the provider CLI publishes
	// it: single, group, list, set or map.
	NestingMode string      `json:"nesting_mode"`
	Block       SchemaBlock `json:"block"`
}

// The five nesting modes the provider CLI publishes for a nested block.
const (
	NestingSingle = "single"
	NestingList   = "list"
	NestingSet    = "set"
	NestingMap    = "map"
	NestingGroup  = "group"
)

// SchemaAttribute records the optionality flags of one argument and the cty
// type the provider published for it.
type SchemaAttribute struct {
	Required bool `json:"required"`
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
	// Type is the argument's cty type in its JSON encoding: "string", or
	// ["list","string"], or ["object",{...}]. It is the normative type source
	// for anything that renders a value back into Terraform syntax: the
	// payload alone cannot tell a set from a list, and `toset(["a"]) == ["a"]`
	// is false.
	Type json.RawMessage `json:"type"`
}

// Block returns the root configuration block of a managed resource or data
// source type, and whether any visible provider describes the type at all.
//
// Block is the document's navigation half: the suggestion renderer walks a
// payload path through nested blocks and their nesting modes, a walk no
// attribute lookup can answer because a nested block is not an attribute.
// Evidence about one argument is Attribute's question.
// Provider selection follows Attribute.
func (s Schemas) Block(kind, resourceType string) (SchemaBlock, bool) {
	schema, known := s.schema(kind, resourceType)

	return schema.Block, known
}

// ProvidersSchema retrieves the provider schemas visible from dir. It suppresses
// colour codes so captured failure diagnostics remain plain text. The limit
// bounds the invocation; an expired limit is an operational failure.
func (r Runner) ProvidersSchema(ctx context.Context, dir string, limit time.Duration) (Schemas, error) {
	result, err := r.runLimited(ctx, dir, limit, "providers schema", "providers", "schema", "-json", "-no-color")
	schemas := Schemas{Usage: result.Usage, Started: result.Started}
	if err != nil {
		return schemas, err
	}

	if result.ExitCode != 0 {
		return schemas, fmt.Errorf("%w: providers schema: %s", ErrCommandFailed, combinedTail(result))
	}

	if err := json.Unmarshal(result.Stdout, &schemas); err != nil {
		return schemas, fmt.Errorf("decoding provider schemas: %w", err)
	}

	return schemas, nil
}

// Attribute is one argument's published schema, as the tool's questions ask
// it. Optional and Computed carry the optionality rules the document's raw
// flags only imply, so a caller never re-derives them against Required.
type Attribute struct {
	// Optional reports whether the configuration may set the argument or
	// omit it: the schema publishes it optional and not required. The
	// deletion operators fire on exactly these arguments — nulling or
	// deleting a required argument is statically doomed and grades
	// nothing.
	Optional bool
	// Computed reports whether the provider fills the argument in: the
	// schema publishes it computed, and not settable by configuration. A
	// mock invents values for exactly these attributes, which is what the
	// oracle's volatility rule and the pinning stage must know.
	Computed bool
	// Type is the argument's cty type in its JSON encoding, exactly as the
	// provider published it: "string", or ["list","string"], or
	// ["object",{...}]. Read it through DeclaredType or ValueType, which
	// carry the two decoding rules; the payload alone cannot tell a set
	// from a list.
	Type json.RawMessage
}

// DeclaredType decodes the cty type the provider published for the argument,
// Terraform's dynamic included. This is the schema's admission evidence —
// the pack entries fire where the replacement is of the declared type — and
// a dynamic argument admits any of them, even though it is no evidence about
// the values the argument holds.
func (a Attribute) DeclaredType() (cty.Type, bool) {
	return decodePublishedType(a.Type)
}

// ValueType decodes the cty type the provider published for the argument,
// with Terraform's dynamic withheld: a dynamic attribute is type evidence
// about the schema and none about the value.
func (a Attribute) ValueType() (cty.Type, bool) {
	decoded, ok := decodePublishedType(a.Type)
	if !ok || decoded == cty.DynamicPseudoType {
		return cty.NilType, false
	}

	return decoded, true
}

// decodePublishedType decodes one argument's published JSON type. An argument
// published with no type, and one whose type does not decode, publishes no
// type evidence at all.
func decodePublishedType(raw json.RawMessage) (cty.Type, bool) {
	if len(raw) == 0 {
		return cty.NilType, false
	}

	decoded, err := ctyjson.UnmarshalType(raw)
	if err != nil {
		return cty.NilType, false
	}

	return decoded, true
}

// Attribute looks up one argument of a managed resource or data source type,
// and whether any visible provider describes the resource type. The path
// descends the type's nested configuration blocks: every segment but the last
// names a nested block, and the last names the argument. Segments contain
// schema names, without instance indices or map keys.
//
// Attribute and Block select the lexicographically first provider address
// whose schema publishes the requested kind and resource type. A missing
// path does not trigger a lookup in another provider's schema.
//
// With a nonempty path, the second result describes whether the resource type
// is known, not whether the argument exists. A missing block or argument on a
// known type returns a zero Attribute and true: Optional and Computed are
// false, and Type is absent. An empty path or unknown resource type returns
// a zero Attribute and false, so callers can distinguish absent arguments
// from missing schema evidence.
func (s Schemas) Attribute(kind, resourceType string, path []string) (Attribute, bool) {
	if len(path) == 0 {
		return Attribute{}, false
	}

	schema, known := s.schema(kind, resourceType)
	if !known {
		return Attribute{}, false // no provider describes it.
	}

	block := schema.Block
	for _, name := range path[:len(path)-1] {
		nested, found := block.BlockTypes[name]
		if !found {
			return Attribute{}, true // no block was described at the path.
		}

		block = nested.Block
	}

	described := block.Attributes[path[len(path)-1]]

	return Attribute{
		Optional: described.Optional && !described.Required,
		Computed: described.Computed && !described.Optional && !described.Required,
		Type:     described.Type,
	}, true
}

func (s Schemas) schema(kind, resourceType string) (Schema, bool) {
	var selected Schema
	var selectedProvider string
	var known bool

	for name, provider := range s.ProviderSchemas {
		schemas := provider.ResourceSchemas
		if kind == dataSourceKind {
			schemas = provider.DataSourceSchemas
		}

		schema, found := schemas[resourceType]
		if found && (!known || name < selectedProvider) {
			selected, selectedProvider, known = schema, name, true
		}
	}

	return selected, known
}
