run "planned" {
  command = plan

  assert {
    condition     = output.left == "left-value" && output.right == "right-value"
    error_message = "both inputs must surface"
  }
}
