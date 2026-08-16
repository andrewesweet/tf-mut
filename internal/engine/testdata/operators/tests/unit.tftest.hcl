run "planned" {
  command = plan

  assert {
    condition     = output.tier == "standard"
    error_message = "the default environment is standard tier"
  }
}
