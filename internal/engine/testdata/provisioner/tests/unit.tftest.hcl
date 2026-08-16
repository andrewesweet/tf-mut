run "applied" {
  command = apply

  assert {
    condition     = output.app == "applied"
    error_message = "apply must surface the input"
  }
}
