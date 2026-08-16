run "applied" {
  command = apply

  assert {
    condition     = terraform_data.token.id != ""
    error_message = "the resource must be created"
  }
}
