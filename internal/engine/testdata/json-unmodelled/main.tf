# Well-formed JSON declaring a construct this version has no reader for. It is
# not a parse failure and it is not permission either: content the tool cannot
# model is content it cannot see, so the floor stays down for it.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
