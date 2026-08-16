resource "terraform_data" "node" {
  count = 2
  input = "node"
}

output "first" {
  value = terraform_data.node[0].input
}
