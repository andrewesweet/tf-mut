# A required input whose validation names the legal values outright. The
# constraint is the module's own statement about which values it accepts, so
# mining it is reading the module rather than guessing at it.

variable "tier" {
  type = string

  validation {
    condition     = contains(["bronze", "silver", "gold"], var.tier)
    error_message = "tier must be one of the named tiers"
  }
}

resource "terraform_data" "network" {
  input = var.tier
}

output "network" {
  value = terraform_data.network.output
}
