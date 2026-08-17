# The #47 review's reproduction shape: the module default proves zero, but an
# automatically loaded *.auto.tfvars sets the gate true and Terraform
# instantiates the block. The evaluator must read the auto-var assignment —
# or fail closed — and never pre-classify from the weaker default.

variable "enabled" {
  type    = bool
  default = false
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

output "instances" {
  value = length(terraform_data.gated)
}
