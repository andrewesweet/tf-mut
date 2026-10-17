run "setup" {
  command = plan

  module {
    source = "./tests/setup"
  }

  assert {
    condition     = output.value == "seeded"
    error_message = "the seed must hold"
  }
}

run "first" {
  command = plan

  variables {
    instance = run.setup.value
  }

  assert {
    condition     = output.echo == run.setup.value
    error_message = "the echo must carry the seeded instance"
  }
}

run "second" {
  command = plan

  variables {
    instance = run.setup.value
  }

  assert {
    condition     = output.echo == run.setup.value
    error_message = "the echo must carry the seeded instance"
  }
}
