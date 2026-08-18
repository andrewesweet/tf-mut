run "applied" {
  command = apply

  assert {
    condition     = output.anchor == "steady"
    error_message = "apply must surface the input"
  }
}
