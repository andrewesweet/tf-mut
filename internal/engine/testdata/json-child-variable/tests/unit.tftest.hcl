run "applied" {
  command = apply

  assert {
    condition     = output.anchor == "steady"
    error_message = "the child must carry the input"
  }
}
