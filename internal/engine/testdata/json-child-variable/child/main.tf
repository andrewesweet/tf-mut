resource "terraform_data" "anchor" {
  input = var.name
}

output "anchor" {
  value = terraform_data.anchor.output
}
