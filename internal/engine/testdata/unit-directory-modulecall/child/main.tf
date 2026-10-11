variable "name" {
  type    = string
  default = "child"
}

output "out" {
  value = var.name
}
