# The census's first preview-refusal fixture: a module whose suite is absent.
#
# `preview` refuses this module because the grading pipeline refuses it: the
# run-block check fires for every grading command, preview included, so no
# report and no mutant count can exist. The census records the population as
# unknown with preview's own refusal text — never as zero.

resource "terraform_data" "subject" {
  input = "old"
}
