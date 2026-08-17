# The other direction of the auto-var repair: terraform.tfvars pins the gate
# false over a true default, so the evaluator proves zero from the file
# Terraform actually reads — pre-classification stays sound.

variable "enabled" {
  type    = bool
  default = true
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

resource "terraform_data" "anchor" {
  input = "steady"
}
