# The F10 static classification: every run that evaluates the module under
# test assigns `instance` explicitly — the setup run retargets another module
# and the two consumers set it from the setup run's output — so the declared
# default is never read and a VAR-DEFAULT mutant of it is statically
# Unobservable, before anything executes.

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
