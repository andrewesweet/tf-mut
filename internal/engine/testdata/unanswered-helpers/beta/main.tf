resource "terraform_data" "bare" {
}

output "ready" {
  value = terraform_data.bare.input == null
}
