run "defaults" {
  command = plan

  assert {
    condition     = output.tier == "standard"
    error_message = "tier is standard"
  }
}
