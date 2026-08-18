# The secret exists only in a failed synthesis attempt: the first validation
# names it outright, so mining finds it, and the second rejects the candidate.
# Every artefact from that failed attempt onwards has to withhold it.

variable "token" {
  type      = string
  sensitive = true

  validation {
    condition     = var.token == "tfmut-diagnostic-secret"
    error_message = "the token must be the issued one"
  }

  validation {
    condition     = can(cidrnetmask(var.token))
    error_message = "unreachable by construction"
  }
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.output
}
