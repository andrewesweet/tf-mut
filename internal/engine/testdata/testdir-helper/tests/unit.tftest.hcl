run "setup" {
  command = plan

  module {
    source = "./tests/setup"
  }

  assert {
    condition     = output.ready == "ready"
    error_message = "the test's own helper must be ready"
  }
}

run "root" {
  command = plan

  assert {
    condition     = output.greeting == "hello"
    error_message = "the module output must hold"
  }
}
