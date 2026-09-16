run "first" {
  command   = apply
  state_key = "shared"
}

run "second" {
  command   = plan
  state_key = "shared"

  variables {
    value = "new"
  }

  assert {
    condition     = terraform_data.subject.input == "old"
    error_message = "a dropped ignore_changes must let the input update"
  }
}
