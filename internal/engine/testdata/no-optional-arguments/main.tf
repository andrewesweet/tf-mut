resource "terraform_data" "bare" {
}

output "identifier" {
  value = terraform_data.bare.id
}
