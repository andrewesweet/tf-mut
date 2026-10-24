run "first" {
  command = plan

  assert {
    condition     = output.token == var.token
    error_message = "the output must echo the token"
  }
}

run "second" {
  command = plan

  assert {
    condition     = output.token == var.token
    error_message = "the second run must still execute"
  }
}
