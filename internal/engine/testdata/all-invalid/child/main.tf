variable "first" {
  type = string
}

variable "second" {
  type = string
}

variable "third" {
  type = string
}

output "joined" {
  value = "${var.first}-${var.second}-${var.third}"
}
