# The count-shaped suggestion fixture: a counted resource nothing asserts
# and nothing reads, beside one the suite asserts. The counted resource's
# delete mutant survives with a proven delta, and the assertion the
# generator writes for that delta indexes the instance the mutant removes —
# so the isolated mutant leg's run errors rather than fails.

resource "terraform_data" "node" {
  count = 1
  input = "node"
}

resource "terraform_data" "checked" {
  input = "checked"
}

output "checked" {
  value = terraform_data.checked.input
}
