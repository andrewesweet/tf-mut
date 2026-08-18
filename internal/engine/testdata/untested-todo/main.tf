# The judgement point the tool refuses to guess at: a required input whose
# constraint states a property rather than a value. Synthesis produces a
# candidate, the constraint rejects it, and what the tool emits is a
# non-executable artefact carrying the constraint verbatim.

variable "vpc_cidr" {
  type = string

  validation {
    condition     = can(cidrnetmask(var.vpc_cidr))
    error_message = "vpc_cidr must be a CIDR block"
  }
}

resource "terraform_data" "network" {
  input = var.vpc_cidr
}

output "network" {
  value = terraform_data.network.output
}
