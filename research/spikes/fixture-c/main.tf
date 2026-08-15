variable "cidr" {
  type    = string
  default = "10.0.0.0/16"
}
module "net" {
  source = "./modules/net"
  cidr   = var.cidr
}
output "subnets" { value = module.net.subnets }
