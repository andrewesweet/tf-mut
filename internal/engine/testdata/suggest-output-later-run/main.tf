variable "flag" {
  type = bool
}

output "aliases" {
  value = var.flag ? [] : ["x"]
}
