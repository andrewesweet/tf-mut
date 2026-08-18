# The cache-measurement fixture (M4d.2, issue #64). Two files with a
# cross-file dependency the simulated per-file key cannot see: `local.orphan`
# lives here, and whether anything observes it is decided in b.tf.

resource "terraform_data" "alpha" {
  input = "alpha-value"
}

locals {
  orphan = "observed-or-not"
}

output "anchor" {
  value = terraform_data.alpha.input
}
