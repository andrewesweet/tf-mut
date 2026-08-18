package tfexec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// dataSourceKind selects the data source half of a provider schema.
const dataSourceKind = "data"

// Schemas is the decoded output of terraform providers schema -json.
type Schemas struct {
	FormatVersion   string                    `json:"format_version"`
	ProviderSchemas map[string]ProviderSchema `json:"provider_schemas"`
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
	Attributes map[string]SchemaAttribute `json:"attributes"`
}

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

// ProvidersSchema retrieves the provider schemas visible from dir.
func (r Runner) ProvidersSchema(ctx context.Context, dir string) (Schemas, error) {
	result, err := r.Run(ctx, dir, "providers", "schema", "-json")
	if err != nil {
		return Schemas{}, err
	}

	if result.ExitCode != 0 {
		return Schemas{}, fmt.Errorf("%w: providers schema: %s", ErrCommandFailed, combinedTail(result))
	}

	schemas := Schemas{}
	if err := json.Unmarshal(result.Stdout, &schemas); err != nil {
		return Schemas{}, fmt.Errorf("decoding provider schemas: %w", err)
	}

	return schemas, nil
}

// Optionality reports whether the named argument of a managed resource or data
// source is schema-optional, and whether the schema knew about it at all.
//
// An argument the schema does not describe is never reported as optional: the
// deletion operators must not fire on a site whose optionality is unknown.
func (s Schemas) Optionality(kind, resourceType, attribute string) (optional, known bool) {
	for _, provider := range s.ProviderSchemas {
		schemas := provider.ResourceSchemas
		if kind == dataSourceKind {
			schemas = provider.DataSourceSchemas
		}

		schema, found := schemas[resourceType]
		if !found {
			continue
		}

		described, found := schema.Block.Attributes[attribute]
		if !found {
			return false, true
		}

		return described.Optional && !described.Required, true
	}

	return false, false
}

// AttributeType returns the cty type the provider published for an argument.
//
// The second result is false where no provider schema types the argument at
// all, including where the published type is Terraform's `dynamic`: a dynamic
// attribute is type evidence about the schema and none about the value.
func (s Schemas) AttributeType(kind, resourceType, attribute string) (cty.Type, bool) {
	described, found := s.attribute(kind, resourceType, attribute)
	if !found || len(described.Type) == 0 {
		return cty.NilType, false
	}

	decoded, err := ctyjson.UnmarshalType(described.Type)
	if err != nil {
		return cty.NilType, false
	}

	if decoded == cty.DynamicPseudoType {
		return cty.NilType, false
	}

	return decoded, true
}

// Computed reports whether the named argument of a managed resource or data
// source is one the provider fills in.
//
// It feeds the oracle's mutation-volatility re-run rule: a mock invents values
// for exactly the computed attributes, so a delta confined to them says the
// mock moved, not the module.
func (s Schemas) Computed(kind, resourceType, attribute string) (computed, known bool) {
	for _, provider := range s.ProviderSchemas {
		schemas := provider.ResourceSchemas
		if kind == dataSourceKind {
			schemas = provider.DataSourceSchemas
		}

		schema, found := schemas[resourceType]
		if !found {
			continue
		}

		described, found := schema.Block.Attributes[attribute]
		if !found {
			return false, true
		}

		return described.Computed && !described.Optional && !described.Required, true
	}

	return false, false
}

// attribute finds one argument's schema across every visible provider.
func (s Schemas) attribute(kind, resourceType, attribute string) (SchemaAttribute, bool) {
	for _, provider := range s.ProviderSchemas {
		schemas := provider.ResourceSchemas
		if kind == dataSourceKind {
			schemas = provider.DataSourceSchemas
		}

		schema, found := schemas[resourceType]
		if !found {
			continue
		}

		described, found := schema.Block.Attributes[attribute]
		if !found {
			return SchemaAttribute{}, false //nolint:exhaustruct // nothing was described.
		}

		return described, true
	}

	return SchemaAttribute{}, false //nolint:exhaustruct // no provider describes it.
}
