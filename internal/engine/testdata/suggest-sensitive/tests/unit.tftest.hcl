variables {
  secret = "s3cret-value"
}

run "applied" {
  command = apply

  assert {
    condition     = output.plain == "visible"
    error_message = "the plain output must be visible"
  }
}
