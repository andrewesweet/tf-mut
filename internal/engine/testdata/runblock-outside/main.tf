# The runblock-outside fixture: a run block whose retargeted module source
# climbs out of the checkout.
#
# The suite's only run retargets a local sibling that is not there: inside a
# checkout the source resolves outside the work tree.

resource "terraform_data" "subject" {
  input = "old"
}

output "subject" {
  value = terraform_data.subject.input
}
