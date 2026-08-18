run "applied" {
  command = apply

  assert {
    condition     = output.anchor == "steady"
    error_message = "the anchor must be applied"
  }

  assert {
    condition     = output.side == "read-from-json"
    error_message = "the JSON-declared resource must carry the HCL local"
  }
}
