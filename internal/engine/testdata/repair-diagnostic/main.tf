# The M5-0.5b diagnostic-shape probe. The judgement point publishes this
# constraint and its module range; a failed staged answer produces a runtime
# diagnostic whose range points at the generated run-block literal instead.

variable "cidr" {
  type = string

  validation {
    condition     = can(cidrnetmask(var.cidr))
    error_message = "Rejected."
  }
}

resource "terraform_data" "subject" {
  input = var.cidr
}

output "subject" {
  value = terraform_data.subject.output
}
