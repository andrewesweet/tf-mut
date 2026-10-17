run "planned" {
  command = plan

  assert {
    condition     = output.subject == "old"
    error_message = "the subject must keep its input"
  }
}
