# One readable JSON file beside one that is not: the floor is per file. The
# readable neighbour's content reaches the inventories and the graph, and the
# unreadable one keeps both safety gates closed until it is removed.

resource "terraform_data" "anchor" {
  input = "steady"
}

locals {
  json_only = "read-from-json"
}

output "anchor" {
  value = terraform_data.anchor.input
}
