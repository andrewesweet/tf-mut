variable "name" {
  type    = string
  default = "child"
}

output "echo" {
  value = var.name
}
