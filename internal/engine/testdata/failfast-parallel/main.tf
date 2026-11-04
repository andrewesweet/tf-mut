# Fail-fast parallel fixture: twelve run blocks execute concurrently with
# independent state, so Terraform's stream interleaves their start, completion,
# plan and assertion events. A failing run's own assertion diagnostics
# therefore arrive after other runs' events, and a fail-fast stop taken at any
# of those discards them. Twelve blocks are enough that Terraform starts some
# of them only after others finish, so start events appear mid-stream as well;
# the regression asserts the retained evidence rather than the interleaving.
# The file after this one proves the stop still ends the child: it is never
# reached, so the record names its truncation.
resource "terraform_data" "watched" {
  input = "kept"
}

output "watched" {
  value = terraform_data.watched.input
}
