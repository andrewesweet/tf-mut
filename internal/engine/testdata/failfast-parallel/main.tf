# Fail-fast parallel fixture: twelve run blocks execute concurrently with
# independent state, so Terraform's stream interleaves their start, completion,
# plan and assertion events. A failing run's own assertion diagnostics
# therefore arrive after other runs' events, and a fail-fast stop taken at any
# of those discards them. Twelve blocks exceed the ten concurrent runs
# Terraform starts at once, so later start events can also appear mid-stream.
resource "terraform_data" "watched" {
  input = "kept"
}

output "watched" {
  value = terraform_data.watched.input
}
