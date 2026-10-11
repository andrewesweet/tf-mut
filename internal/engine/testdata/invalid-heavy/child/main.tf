variable "first" {
  type = string
}

variable "second" {
  type = string
}

variable "third" {
  type = string
}

variable "optional" {
  type    = string
  default = "default"
}

output "joined" {
  value = "${var.first}-${var.second}-${var.third}-${var.optional}"
}
