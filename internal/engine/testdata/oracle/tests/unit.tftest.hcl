run "applied" {
  command = apply

  assert {
    condition     = output.tier == "standard"
    error_message = "the default environment is not production"
  }
}
