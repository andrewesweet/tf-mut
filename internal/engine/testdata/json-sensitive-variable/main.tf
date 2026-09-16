# The discover-only promise where a declaration removes a mutant: the output's
# sensitivity flip is skipped when the output reads a variable declared
# sensitive, and here that declaration lives in a `.tf.json` file. Reading it
# must not remove the mutant the unread closure has.

resource "terraform_data" "anchor" {
  input = var.secret
}

output "secret" {
  value     = var.secret
  sensitive = true
}
