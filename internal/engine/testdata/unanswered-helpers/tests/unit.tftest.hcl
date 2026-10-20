run "setup" {
  command = plan

  module {
    source = "./tests/helper"
  }

  assert {
    condition     = output.ready
    error_message = "the test's own helper must be ready"
  }
}

run "root" {
  command = plan

  assert {
    condition     = module.alpha.ready && module.beta.ready
    error_message = "both children must surface their inputs"
  }
}
