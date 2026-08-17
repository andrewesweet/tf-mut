run "planned" {
  command = plan

  assert {
    condition     = terraform_data.app.input == "fixed"
    error_message = "the input must be fixed"
  }
}
