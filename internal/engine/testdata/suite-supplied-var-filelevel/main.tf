# The file-level half of the F10 classification: a variables block at the top
# of the test file reaches every run in it, so the declared default of
# `instance` is never read and a VAR-DEFAULT mutant of it is statically
# Unobservable.

variable "instance" {
  type    = string
  default = "builtin-default"
}

resource "terraform_data" "app" {
  input = var.instance
}

output "echo" {
  value = terraform_data.app.input
}
