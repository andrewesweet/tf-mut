# The R2-9 reproduction: a volatile prefix with an assertable stable suffix.
#
# Masking the whole value would erase a real finding, so the mask keeps the
# literal components the syntax shows around the impure call.

resource "terraform_data" "token" {
  input = "${uuid()}-stable"
}

# uuidv5 is deterministic, so nothing about it may be masked: a mutation of its
# namespace has to stay killable.
resource "terraform_data" "derived" {
  input = uuidv5("dns", "example.com")
}

output "token" {
  value = terraform_data.token.input
}

output "derived" {
  value = terraform_data.derived.input
}
