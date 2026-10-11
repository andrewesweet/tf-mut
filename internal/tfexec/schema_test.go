package tfexec_test

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The provider CLI's schema document is a wire contract, and the nested-block
// half of it is what the rendering walk reads. The shapes below are what
// `terraform providers schema -json` emits for a provider with nested blocks,
// reduced to the members the walk consumes; the end-to-end cases live in the
// engine suite against the real binary.
func TestASchemaWithNestedBlocksDecodesAndLooksUp(t *testing.T) {
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
	              "timeouts": {"nesting_mode": "single", "block": {}}
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

	block, found := schemas.Block("resource", "example_thing")
	if !found {
		t.Fatal("Block(resource, example_thing) was not found")
	}

	if len(block.BlockTypes) != 2 {
		t.Fatalf("block types = %d, want 2", len(block.BlockTypes))
	}

	connection := block.BlockTypes["connection"]
	if connection.NestingMode != tfexec.NestingList {
		t.Fatalf("connection nesting = %q, want %q", connection.NestingMode, tfexec.NestingList)
	}

	if _, found := connection.Block.Attributes["subresource_names"]; !found {
		t.Fatal("the nested block's attributes did not decode")
	}

	data, found := schemas.Block("data", "example_lookup")
	if !found || len(data.BlockTypes) != 0 {
		t.Fatalf("Block(data, example_lookup) = (%+v, %v), want a block with no nested blocks", data, found)
	}

	if _, found := schemas.Block("resource", "example_absent"); found {
		t.Fatal("Block(resource, example_absent) was found")
	}

	tags, known := tfexec.BlockAttributeType(block, "tags")
	if !known || !tags.IsMapType() || tags.ElementType() != cty.String {
		t.Fatalf("BlockAttributeType(tags) = (%v, %v), want map(string)", tags, known)
	}

	if _, known := tfexec.BlockAttributeType(block, "absent"); known {
		t.Fatal("BlockAttributeType(absent) was known")
	}
}
