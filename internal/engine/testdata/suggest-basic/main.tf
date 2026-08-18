# The suggestion engine's demo module: one resource the suite asserts and one
# it does not. Every mutant of the second survives with a proven delta, which is
# exactly the input the generator turns into the assertion that kills it.

resource "terraform_data" "asserted" {
  input = "asserted"
}

resource "terraform_data" "ignored" {
  input = "nobody-checks-me"
}

output "asserted" {
  value = terraform_data.asserted.input
}

output "ignored" {
  value = terraform_data.ignored.input
}
