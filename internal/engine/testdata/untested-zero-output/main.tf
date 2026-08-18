# A module with no outputs at all. At the outputs rung a characterisation of it
# would pin nothing and report success, which is the false confidence the
# zero-output contract exists to prevent.

variable "replicas" {
  type    = number
  default = 2
}

resource "terraform_data" "unit" {
  count = var.replicas

  input = "unit-${count.index}"
}
