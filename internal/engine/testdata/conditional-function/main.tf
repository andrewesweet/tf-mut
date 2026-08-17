# Fail-closed category three (M3a.3): a function call is outside the supported
# expression forms, so pre-classification fails closed to execution even
# though the value is zero on every run.

resource "terraform_data" "gated" {
  count = length([])

  input = "gated-body"
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
