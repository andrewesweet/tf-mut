run "applied" {
  command = apply

  assert {
    condition     = output.greeting == "hello"
    error_message = "greeting must carry its value"
  }

  assert {
    condition     = output.config.enabled == true
    error_message = "config must stay enabled"
  }
}
