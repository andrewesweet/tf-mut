run "applied" {
  command = apply

  assert {
    condition     = output.anchor == "steady"
    error_message = "the anchor must be applied"
  }
}
