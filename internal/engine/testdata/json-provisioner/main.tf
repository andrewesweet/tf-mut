# The floor's unsandboxed-effects case, and issue #57's first reproduction: the
# only provisioner in the closure is declared in `effects.tf.json`, so it never
# enters the effect inventory while that file is unread.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
