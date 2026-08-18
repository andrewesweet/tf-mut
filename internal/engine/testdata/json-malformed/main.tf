# A JSON file the tool cannot decode must keep the floor down rather than lift
# it: a parse failure is never permission.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
