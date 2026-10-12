package suggest_test

import (
	"encoding/json"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// TestAWireSchemaServesTheRenderingWalk pins the decode half of the nested-
// block contract in the same place its rendering half is pinned. The document
// below is recorded real `terraform providers schema -json` output, reduced
// to the members the walk consumes; the decoded value is exercised through
// the same rendering cases the adapter matrix runs, and the engine suite
// carries the end-to-end cases against the real binary.
func TestAWireSchemaServesTheRenderingWalk(t *testing.T) {
	t.Parallel()

	document := `{
	  "format_version": "1.0",
	  "provider_schemas": {
	    "registry.terraform.io/example/example": {
	      "resource_schemas": {
	        "example_thing": {
	          "block": {
	            "attributes": {
	              "tags": {"optional": true, "type": ["map", "string"]}
	            },
	            "block_types": {
	              "connection": {
	                "nesting_mode": "list",
	                "block": {
	                  "attributes": {
	                    "subresource_names": {"optional": true, "type": ["list", "string"]}
	                  }
	                }
	              },
	              "timeouts": {"nesting_mode": "single", "block": {
	                "attributes": {"create": {"optional": true, "type": "string"}}
	              }}
	            }
	          }
	        }
	      },
	      "data_source_schemas": {
	        "example_lookup": {"block": {"attributes": {}}}
	      }
	    }
	  }
	}`

	schemas := tfexec.Schemas{}
	if err := json.Unmarshal([]byte(document), &schemas); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	for _, testCase := range []renderingCase{
		{
			name:     "a list-nested block's attribute, decoded from the wire",
			path:     statePrefix + "example_thing.app].values.connection[0].subresource_names[0]",
			baseline: secondValue,
			schemas:  schemas,
			want:     `example_thing.app.connection[0].subresource_names[0] == "second"`,
			refused:  nil,
		},
		{
			name:     "a single-nested block's attribute, decoded from the wire",
			path:     statePrefix + "example_thing.app].values.timeouts.create",
			baseline: `"5m"`,
			schemas:  schemas,
			want:     `example_thing.app.timeouts.create == "5m"`,
			refused:  nil,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			change := changeAt(testCase.path, testCase.baseline)
			change.Mutant = testCase.mutant

			expression, err := suggest.Express(
				testCase.schemas, testCase.evidence, change,
			)

			assertAdapter(t, expression, err, testCase.want, testCase.refused)
		})
	}
}

func TestNestedSchemaPathsPreserveRendering(t *testing.T) {
	t.Parallel()

	schemas := schemaBlock(nil, map[string]tfexec.SchemaBlockType{
		"connection": {NestingMode: tfexec.NestingList, Block: tfexec.SchemaBlock{
			BlockTypes: map[string]tfexec.SchemaBlockType{
				"timeouts": {NestingMode: tfexec.NestingGroup, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"create": {Optional: true, Type: json.RawMessage(`"string"`)},
					},
				}},
				"mounts": {NestingMode: tfexec.NestingMap, Block: tfexec.SchemaBlock{
					Attributes: map[string]tfexec.SchemaAttribute{
						"sources": {Optional: true, Type: json.RawMessage(`["list","string"]`)},
					},
				}},
			},
		}},
	})
	for _, provider := range schemas.ProviderSchemas {
		provider.DataSourceSchemas["example_lookup"] = provider.ResourceSchemas["example_thing"]
	}

	for _, resource := range []string{"example_thing.app", "data.example_lookup.app"} {
		for _, testCase := range []struct {
			path     string
			baseline string
			want     string
		}{
			{
				path:     "connection[0].timeouts.create",
				baseline: `"5m"`,
				want:     `.connection[0].timeouts.create == "5m"`,
			},
			{
				path:     "connection[0].mounts.shared.sources[0]",
				baseline: secondValue,
				want:     `.connection[0].mounts["shared"].sources[0] == "second"`,
			},
		} {
			t.Run(resource+"/"+testCase.path, func(t *testing.T) {
				t.Parallel()

				change := changeAt(statePrefix+resource+"].values."+testCase.path, testCase.baseline)
				expression, err := suggest.Express(schemas, suggest.Evidence{}, change)

				assertAdapter(t, expression, err, resource+testCase.want, nil)
			})
		}
	}
}

func TestOverlappingProviderSchemasPreserveRendering(t *testing.T) {
	t.Parallel()

	provider := func(hasLabels bool) tfexec.ProviderSchema {
		attributes := map[string]tfexec.SchemaAttribute{}
		if hasLabels {
			attributes["resource_labels"] = tfexec.SchemaAttribute{
				Optional: true, Type: json.RawMessage(`["map","string"]`),
			}
		}

		schema := tfexec.Schema{Block: tfexec.SchemaBlock{
			BlockTypes: map[string]tfexec.SchemaBlockType{
				"node_config": {NestingMode: tfexec.NestingList, Block: tfexec.SchemaBlock{
					Attributes: attributes,
				}},
			},
		}}

		return tfexec.ProviderSchema{
			ResourceSchemas:   map[string]tfexec.Schema{"example_thing": schema},
			DataSourceSchemas: map[string]tfexec.Schema{"example_lookup": schema},
		}
	}

	for _, testCase := range []struct {
		name              string
		selectedHasLabels bool
		refused           error
	}{
		{name: "selected provider describes the attribute", selectedHasLabels: true, refused: nil},
		{name: "only the other provider describes the attribute", selectedHasLabels: false, refused: suggest.ErrUnrenderable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			schemas := tfexec.Schemas{ProviderSchemas: map[string]tfexec.ProviderSchema{
				"registry.terraform.io/example/a": provider(testCase.selectedHasLabels),
				"registry.terraform.io/example/z": provider(!testCase.selectedHasLabels),
			}}

			for _, resource := range []string{"example_thing.app", "data.example_lookup.app"} {
				want := ""
				if testCase.selectedHasLabels {
					want = resource + `.node_config[0].resource_labels.env == "steady"`
				}

				change := changeAt(statePrefix+resource+"].values.node_config[0].resource_labels.env", steadyBaseline)
				for range 128 {
					expression, err := suggest.Express(schemas, suggest.Evidence{}, change)
					assertAdapter(t, expression, err, want, testCase.refused)
				}
			}
		})
	}
}
