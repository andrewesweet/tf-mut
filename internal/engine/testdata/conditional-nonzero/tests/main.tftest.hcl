run "on" {
  command = plan

  variables {
    enabled = true
  }

  assert {
    condition     = length(terraform_data.gated) == 1
    error_message = "the gated resource must be instantiated"
  }

  assert {
    condition     = terraform_data.gated[0].input == "gated-body"
    error_message = "the body must be planned as written"
  }
}
