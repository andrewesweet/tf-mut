# Fail-fast error fixture: the variable's validation rejects the value the
# string mutant leaves behind, so the mutant's first run errors where a fail
# run would have stopped the invocation. The second run block records the
# skip Terraform reports after an error — the record a stop-on-error would
# lose, and the observable proof that fail-fast does not fire here.
variable "token" {
  type    = string
  default = "abc-123"

  validation {
    condition     = can(regex("^[a-z]{3}-[0-9]{3}$", var.token))
    error_message = "the token must look like abc-123"
  }
}

resource "terraform_data" "subject" {
  input = var.token
}

output "token" {
  value = terraform_data.subject.input
}
