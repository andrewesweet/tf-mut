variable "needed" {
  type    = string
  default = "child default"
}

output "echo" {
  value = var.needed
}
