# The census's gated-preview fixture: a module whose population preview can
# count and whose grading run the safety gates refuse.
#
# The module carries a local-exec provisioner and its suite declares an
# apply-mode run, so `run` refuses with the unsandboxed-effects gate before
# anything executes — while `preview`, which executes nothing, reports the
# population. The two facts are decided by different invocations, which is
# the census's availability-versus-row-outcome separation. The provisioner's
# command is never executed: the gates are decided statically before any
# Terraform runs.

resource "terraform_data" "subject" {
  input = "old"

  provisioner "local-exec" {
    command = "exit 99"
  }
}
