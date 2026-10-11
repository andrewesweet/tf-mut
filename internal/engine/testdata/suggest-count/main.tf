# The count-shaped suggestion fixture: an unasserted output exposes the
# counted resource's inputs as a list, beside one the suite asserts. The
# counted resource's delete mutant survives with a proven delta, and the
# generated assertion indexes the output element the mutant removes — so
# the isolated mutant leg's run errors rather than fails.

resource "terraform_data" "node" {
  count = 1
  input = "node"
}

output "nodes" {
  value = terraform_data.node[*].input
}

resource "terraform_data" "checked" {
  input = "checked"
}

output "checked" {
  value = terraform_data.checked.input
}
