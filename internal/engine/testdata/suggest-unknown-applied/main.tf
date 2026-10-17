# The known-to-unknown applied fixture: the same conditional routing as the
# plan fixture, exercised under a `command = apply` run, where both branches
# evaluate and every delta the suggest oracle sees is known-to-known.
resource "terraform_data" "flag" {
  input = true
}

resource "terraform_data" "other" {
  input = "deep"
}

output "echo" {
  value = terraform_data.flag.input ? "on" : terraform_data.other.output
}
