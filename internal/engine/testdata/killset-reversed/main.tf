# One mutable site observed by two assertions, so a single mutant fails both.

resource "terraform_data" "app" {
  input = "kept"
}

output "app" {
  value = terraform_data.app.output
}

output "twin" {
  value = terraform_data.app.input
}
