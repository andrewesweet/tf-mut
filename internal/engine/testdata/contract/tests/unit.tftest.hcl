run "applied" {
  command = apply

  assert {
    condition     = output.both == "one-two"
    error_message = "both inputs must be present"
  }
}
