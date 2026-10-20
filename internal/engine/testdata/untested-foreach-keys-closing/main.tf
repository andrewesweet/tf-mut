# A for_each whose keys carry the very brackets an instance address is written
# inside. The payload quotes each key between the address's brackets, so the
# counts rung's key-set pin only pins the truth if the address is cut at its
# matching close; cutting at the first `]` cuts the key, and the generated
# suite fails its own verification.

resource "terraform_data" "item" {
  for_each = {
    "plain"    = 1
    "brack]et" = 2
    "brack[et" = 3
  }

  input = each.key
}

output "keys" {
  value = sort(keys(terraform_data.item))
}
