# The suite-supplied JSON case: the only run the suite carries is declared in
# JSON and assigns var.instance through the run's own variables block. The
# inventory holds no assignment for a JSON-declared run — the same
# conservatism the multiplicity evaluator applies — so the default keeps
# today's execution whatever the bytes say.
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
