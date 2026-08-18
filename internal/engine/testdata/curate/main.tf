# A suite with one assertion that senses something and one that senses nothing:
# the second reads a constant the module computes from no mutable site, so no
# mutant's death can ever depend on it.

variable "env" {
  type    = string
  default = "dev"
}

resource "terraform_data" "app" {
  input = "app-${var.env}"
}

output "app" {
  value = terraform_data.app.output
}

output "constant" {
  value = "unchanging"
}
