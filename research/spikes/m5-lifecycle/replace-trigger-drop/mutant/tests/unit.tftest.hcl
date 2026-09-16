run "first" {
  command   = apply
  state_key = "shared"
}

run "second" {
  command   = apply
  state_key = "shared"

  variables {
    revision = "v2"
  }

  assert {
    condition     = terraform_data.subject.id != run.first.subject_id
    error_message = "a trigger change must replace the subject"
  }
}
