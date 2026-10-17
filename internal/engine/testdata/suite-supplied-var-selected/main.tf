# The selection half of the F10 classification: the rule quantifies over the
# runs the current filter actually selects. One test file sets `instance` in
# every run; the other omits it, so the verdict follows the selection.

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
