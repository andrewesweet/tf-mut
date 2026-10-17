# The setup run's own module: retargeted, so it never evaluates the module
# under test and never reads the variable whose default the suite supplies.

resource "terraform_data" "seed" {
  input = "seeded"
}

output "value" {
  value = terraform_data.seed.input
}
