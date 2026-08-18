# The round-3 review's critical reproduction: the secret sits at index 1 of a
# collection whose index 0 the module exposes. A collection mutant
# (COLL-DROP-FIRST, COLL-REVERSE, IDX-SHIFT) moves the secret into a path whose
# baseline value was public — so the mutant payload, not the baseline, is where
# Terraform marks it, and a sensitivity predicate decided over the baseline
# alone prints the secret in the delta evidence.

variable "secret" {
  type      = string
  sensitive = true
}

locals {
  values = ["visible", var.secret]
}

resource "terraform_data" "tier" {
  input = local.values[0]
}

resource "terraform_data" "anchor" {
  input = "steady"
}

# Deliberately no output over tier: an unmarked output of a sensitive value is
# its own Terraform error, which would kill the very mutants this fixture
# needs to survive.
output "anchor" {
  value = terraform_data.anchor.input
}
