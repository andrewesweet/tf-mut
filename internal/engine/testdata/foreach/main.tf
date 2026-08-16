resource "terraform_data" "node" {
  for_each = toset(["a", "b"])
  input    = each.key
}

output "size" {
  value = length(keys(terraform_data.node))
}
