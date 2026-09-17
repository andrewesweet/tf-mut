run "plan" {
  command = plan

  assert {
    condition     = terraform_data.subject.input == "old"
    error_message = "subject must keep its input"
  }
}
