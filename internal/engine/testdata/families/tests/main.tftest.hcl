run "planned" {
  command = plan

  assert {
    condition     = terraform_data.app.input.chosen == 1
    error_message = "min must choose the smaller value"
  }

  assert {
    condition     = terraform_data.app.input.label == "Name"
    error_message = "title must capitalise the label"
  }
}
