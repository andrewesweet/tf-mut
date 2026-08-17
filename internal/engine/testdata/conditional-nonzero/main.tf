# The decidable-to-nonzero control (M3a.3): the evaluator can decide the
# multiplicity — and it is one, not zero — so pre-classification abstains and
# every body mutant executes to its ordinary verdict.

variable "enabled" {
  type    = bool
  default = true
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

output "gated" {
  value = terraform_data.gated[0].input
}
