# A lifecycle-persistent value makes shared state observable across generated
# scenarios. Distinct state keys preserve each scenario's configured value.

variable "env" {
  type    = string
  default = "dev"
}

locals {
  tier = var.env == "prod" ? "critical" : "standard"
}

resource "terraform_data" "unit" {
  input = local.tier

  lifecycle {
    ignore_changes = [input]
  }
}

output "tier" {
  value = terraform_data.unit.output
}
