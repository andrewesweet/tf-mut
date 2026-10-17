# The conservative direction of the F10 classification: one run that evaluates
# the module under test omits `instance`, so that run reads the declared
# default and a VAR-DEFAULT mutant of it executes as it always has.

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
