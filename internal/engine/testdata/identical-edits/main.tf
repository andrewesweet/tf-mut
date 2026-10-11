# Two positions in one attribute that rewrite identically: the same numeric
# literal in both conditions of one local, and a list of identical strings.
# The position-free identity hash cannot tell the rewrites apart, so each pair
# needs an occurrence ordinal.

variable "a" {
  type    = number
  default = 1
}

locals {
  pick = var.a == 1 ? "first" : (var.a == 1 ? "second" : "third")
}

resource "terraform_data" "values" {
  input = ["same", "same"]
}

output "pick" {
  value = local.pick
}
