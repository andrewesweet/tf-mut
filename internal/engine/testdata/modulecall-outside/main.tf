# The modulecall-outside fixture: an ordinary module call whose local source
# climbs out of the checkout.
#
# The call is not a run-block retarget: the closure follows it as an ordinary
# local module call, and inside a checkout the source resolves outside the
# work tree — a directory the sandbox copy can never contain.

resource "terraform_data" "subject" {
  input = "old"
}

output "subject" {
  value = terraform_data.subject.input
}

module "outside" {
  source = "../outside-sibling"
}
