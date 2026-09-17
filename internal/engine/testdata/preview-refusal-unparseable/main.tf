# The census's third preview-refusal fixture: a module whose source does not
# parse.
#
# Discovery refuses the module, so no report and no mutant count can exist and
# the census records the population as unknown with preview's own refusal
# text. The malformed expression is deliberate; the module is never formatted
# or repaired because its unparseability is the fixture's point.

resource "terraform_data" "subject" {
  input =
}
