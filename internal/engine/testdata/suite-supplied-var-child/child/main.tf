variable "instance" {
  type    = string
  default = "child-default"
}

resource "terraform_data" "leaf" {
  input = var.instance
}

output "echo" {
  value = terraform_data.leaf.input
}
