# The unseen-source half of the F10 classification: an automatically loaded
# variable file is a source the test-file inventory does not see, so the
# classification stays silent and a VAR-DEFAULT mutant of `instance` executes
# as it always has, even though every run also sets it in the test files.

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
