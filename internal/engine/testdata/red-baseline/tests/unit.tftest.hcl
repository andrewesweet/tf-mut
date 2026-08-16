run "wrong_expectation" {
  command = plan

  assert {
    condition     = output.tier == "critical"
    error_message = "this expectation is wrong on purpose"
  }
}
