# The C1 fixture (M3a.3): every relevant run pins var.enabled to false and
# var.items to empty, so the mutated multiplicity expressions of both gated
# blocks are statically zero — a body-only mutant is NoCoverage without an
# execution, while a mutant in or upstream of the condition always executes.

variable "enabled" {
  type    = bool
  default = true
}

variable "items" {
  type    = map(string)
  default = { seed = "value" }
}

resource "terraform_data" "gated" {
  count = var.enabled ? 1 : 0

  input = "gated-body"
}

resource "terraform_data" "each_gated" {
  for_each = var.items

  input = each.value
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
