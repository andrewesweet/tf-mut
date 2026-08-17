# Fail-closed category one (M3a.3): a run with plan_options.target changes
# what a run instantiates in a way the evaluator does not model, so
# pre-classification fails closed to execution for every mutant.

variable "enabled" {
  type    = bool
  default = false
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

resource "terraform_data" "anchor" {
  input = "steady"
}
