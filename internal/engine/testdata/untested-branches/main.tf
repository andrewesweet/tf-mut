# A conditional a variable decides. Branch expansion synthesises the value that
# flips it, so the characterisation pins both sides rather than only the one the
# default happens to take.

variable "env" {
  type    = string
  default = "dev"
}

locals {
  replicas = var.env == "prod" ? 2 : 1
}

resource "terraform_data" "unit" {
  count = local.replicas

  input = "${var.env}-${count.index}"
}

output "units" {
  value = [for unit in terraform_data.unit : unit.output]
}

output "tier" {
  value = var.env == "prod" ? "critical" : "standard"
}
