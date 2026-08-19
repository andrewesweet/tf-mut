# A for_each whose keys carry the characters an HCL string has to escape. The
# generated suite quotes them in an assertion condition and in the diagnostic
# beside it, so a renderer that builds either by concatenation produces a file
# Terraform cannot parse.

resource "terraform_data" "item" {
  for_each = {
    "plain"          = 1
    "quo\"ted"       = 2
    "dollar$${brace" = 3
  }

  input = each.key
}

output "keys" {
  value = sort(keys(terraform_data.item))
}
