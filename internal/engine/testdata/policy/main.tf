resource "terraform_data" "graded" {
  input = "watched"
}

resource "terraform_data" "debug" {
  input = "excluded by address"
}

resource "terraform_data" "reasoned" {
  # tf-mut:disable STR-EMPTY — the empty string is normalised downstream
  input = "suppressed"
}

resource "terraform_data" "reasonless" {
  # tf-mut:disable STR-EMPTY
  input = "still reported"
}

resource "terraform_data" "precise" {
  # Two operators fire on the following line; only the named one is suppressed.
  # tf-mut:disable NULL-INJECT — the provider rejects a null input anyway
  input = "two operators here"
}

output "graded" {
  value = terraform_data.graded.input
}
