variable "needed" {
  type = string
}

locals {
  shaped = upper(var.needed)
}

output "echo" {
  value = local.shaped
}
