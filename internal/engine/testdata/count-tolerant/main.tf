variable "size" {
  type    = number
  default = 2
}

resource "terraform_data" "node" {
  count = var.size
  input = "node"
}

output "size" {
  value = length(terraform_data.node)
}
