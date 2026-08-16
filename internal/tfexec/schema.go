package tfexec

import (
	"context"
	"encoding/json"
	"fmt"
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

// SchemaAttribute records the optionality flags of one argument.
type SchemaAttribute struct {
	Required bool `json:"required"`
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
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

// Computed reports whether the named argument of a managed resource or data
// source is one the provider fills in.
//
// It is the static half of the `mock-masked` diagnosis: a mock invents values
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
