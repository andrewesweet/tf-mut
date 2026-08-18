# The floor's real-infrastructure case: the module's only provider requirement
# and its only provider-backed resource live in `providers.tf.json`, which the
# `.tf`-only discovery cannot read. Terraform reads it, because a sandbox copies
# the whole closure, so without the floor this run reaches a real `null` provider
# without either gate ever seeing it.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
