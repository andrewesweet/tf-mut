run "planned" {
  command = plan

  assert {
    condition     = output.graded == "watched"
    error_message = "the graded input must survive"
  }
}
