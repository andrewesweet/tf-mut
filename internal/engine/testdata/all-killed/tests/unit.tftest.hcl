run "defaults" {
  command = plan

  assert {
    condition     = output.tier == "standard"
    error_message = "tier must be standard"
  }
}
