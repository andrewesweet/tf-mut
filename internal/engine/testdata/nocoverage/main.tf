resource "terraform_data" "never_planned" {
  input = "root"
}

output "root_only" {
  value = terraform_data.never_planned.input
}
