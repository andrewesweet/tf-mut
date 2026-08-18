resource "terraform_data" "child_only" {
  input = "child-value"
}

output "child_only" {
  value = terraform_data.child_only.input
}
