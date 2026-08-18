# The JSON-declared module-call reproduction (PR #69 review): the only calls to
# both children live in calls.tf.json, so a closure followed through the HCL
# syntax alone would never inventory the children's provider or provisioner —
# and both safety gates would fail open through the side door.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
