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
