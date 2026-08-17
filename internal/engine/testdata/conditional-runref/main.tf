# Fail-closed category two (M3a.3): a run-level variable fed by a run.*
# reference is outside the evaluator's enumerated context, so
# pre-classification fails closed to execution.

variable "enabled" {
  type    = bool
  default = false
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

output "gate" {
  value = false
}
