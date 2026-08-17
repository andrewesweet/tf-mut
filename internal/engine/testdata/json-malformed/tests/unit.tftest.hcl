run "planned" {
  command = plan

  assert {
    condition     = output.anchor == "steady"
    error_message = "the anchor must be planned"
  }
}
