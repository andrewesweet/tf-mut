# The floor's static-evaluation case: `terraform.tfvars.json` decides the gate
# Terraform will actually apply, and the evaluator cannot read it. The
# conditional-instantiation shortcut must not pre-classify anything here.

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
