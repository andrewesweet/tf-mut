# A secret the reader has to supply: no default, and a constraint no
# deterministic synthesis satisfies. The answer has to reach Terraform, because
# a plan cannot be run against a redaction marker, and it has to reach no
# report field, because it is a secret.

variable "token" {
  type      = string
  sensitive = true

  validation {
    condition     = can(regex("^tok-[0-9a-f]{8}$", var.token))
    error_message = "the token must be an issued token identifier"
  }
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.output
}
