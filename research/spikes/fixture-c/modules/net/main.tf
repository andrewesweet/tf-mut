variable "cidr" { type = string }
variable "azs" {
  type    = list(string)
  default = ["a", "b"]
}
locals {
  subnets = [for i, az in var.azs : cidrsubnet(var.cidr, 8, i)]
}
output "subnets" { value = local.subnets }
output "count" { value = length(local.subnets) }
