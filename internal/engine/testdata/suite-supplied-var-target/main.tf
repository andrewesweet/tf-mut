# The suite-supplied targeted case: the run sets var.instance and carries a
# plan_options target. A targeted plan's surviving graph is not modelled — the
# multiplicity evaluator refuses it the same way — so the default keeps
# today's execution.
variable "instance" {
  type    = string
  default = "default-value"
}

resource "terraform_data" "app" {
  input = var.instance
}

output "instance" {
  value = terraform_data.app.input
}
