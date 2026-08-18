# A JSON run block carrying an argument this version does not model
# (expect_failures) must leave the file unread and the floor down: run
# arguments can change what an execution outcome means.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
