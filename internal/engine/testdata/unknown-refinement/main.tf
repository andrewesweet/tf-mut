# The R2-2 refutation, as a module.
#
# `terraform_data.derived.input` is unknown at plan time, but cty refines it
# with a known prefix that the plan serialisation discards. Mutating the prefix
# therefore produces a byte-identical plan document — and a `startswith`
# assertion still kills it. The oracle must never call this unobservable.

locals {
  prefix = "stable-"
}

variable "suffix" {
  type    = string
  default = "one"
}

resource "terraform_data" "source" {
  input = var.suffix
}

resource "terraform_data" "derived" {
  input = "${local.prefix}${terraform_data.source.output}"
}

output "derived" {
  value = terraform_data.derived.input
}
