# Both indeterminacy predicates hold at once: a plan-mode payload always carries
# unknown values, and the conditional exposes volatility only under mutation.
# The higher diagnosis has to win.

variable "stable" {
  type    = bool
  default = true
}

resource "terraform_data" "seed" {
  input = "seed"
}

resource "terraform_data" "token" {
  input = var.stable ? "fixed" : uuid()

  triggers_replace = terraform_data.seed.output
}

output "token" {
  value = terraform_data.token.input
}
