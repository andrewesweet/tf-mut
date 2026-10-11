run "picks_first" {
  command = plan

  variables {
    a = 1
  }

  assert {
    condition     = output.pick == "first"
    error_message = "the first condition must win while var.a is 1"
  }
}
