run "defaults" {
  command = plan

  assert {
    condition     = output.asserted == "kept"
    error_message = "the asserted output must keep its value"
  }
}
