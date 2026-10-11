# The runblock-outside fixture: a run block whose retargeted module source
# climbs out of the checkout.
#
# The suite's only run retargets a local sibling that is not there: inside a
# checkout the source resolves outside the work tree, and outside one it
# resolves to nothing at all. Both shapes once surfaced as the raw read error
# of the directory discovery went on to open; they are refused with the run
# block and the source string named instead.

resource "terraform_data" "subject" {
  input = "old"
}

output "subject" {
  value = terraform_data.subject.input
}
