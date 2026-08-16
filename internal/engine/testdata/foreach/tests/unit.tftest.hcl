run "defaults" {
  command = plan

  assert {
    condition     = output.size == 2
    error_message = "two nodes"
  }
}
