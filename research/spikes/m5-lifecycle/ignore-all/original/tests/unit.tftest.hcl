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
    condition     = terraform_data.subject.input == "new"
    error_message = "ignore_changes = all must hold the input at its old value"
  }
}
