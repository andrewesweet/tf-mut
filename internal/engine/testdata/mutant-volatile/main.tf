# The C4 reproduction: volatility that exists only under mutation.
#
# Both baseline runs take the stable arm, so the two-run diff sees nothing to
# mask. A mutant that collapses the conditional to the other arm makes the value
# move on every run — volatility the baseline could not have observed.

variable "stable" {
  type    = bool
  default = true
}

resource "terraform_data" "token" {
  input = var.stable ? "fixed" : uuid()
}

output "token" {
  value = terraform_data.token.input
}
