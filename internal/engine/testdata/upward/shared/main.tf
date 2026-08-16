variable "prefix" {
  type    = string
  default = "shared"
}

output "name" {
  value = "${var.prefix}-network"
}
