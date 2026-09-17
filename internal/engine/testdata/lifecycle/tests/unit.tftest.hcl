run "drop_first" {
  command   = apply
  state_key = "drop"
}

run "drop_second" {
  command   = plan
  state_key = "drop"

  variables {
    value = "new"
  }

  assert {
    condition     = terraform_data.held.input == "old"
    error_message = "a dropped ignore_changes must let the input update"
  }
}

run "all_first" {
  command   = apply
  state_key = "all"
}

run "all_second" {
  command   = plan
  state_key = "all"

  variables {
    value = "new"
  }

  assert {
    condition     = terraform_data.wide.input == "new"
    error_message = "ignore_changes = all must hold the input at its old value"
  }
}

run "replace_first" {
  command   = apply
  state_key = "replace"
}

run "replace_second" {
  command   = apply
  state_key = "replace"

  variables {
    revision = "v2"
  }

  assert {
    condition     = terraform_data.replaced.id != run.replace_first.replaced_id
    error_message = "a trigger change must replace the subject"
  }
}
