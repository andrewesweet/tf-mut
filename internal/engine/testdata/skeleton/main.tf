resource "terraform_data" "asserted" {
  input = "kept"
}

resource "terraform_data" "ignored" {
  input = "unwatched"
}

output "asserted" {
  value = terraform_data.asserted.input
}

output "unasserted" {
  value = terraform_data.ignored.input
}
