run "applied" {
  command = apply

  assert {
    condition     = output.anchor == "alpha-value"
    error_message = "the anchor must carry its input"
  }
}
