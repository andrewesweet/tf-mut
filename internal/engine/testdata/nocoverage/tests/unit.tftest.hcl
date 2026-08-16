run "child_only" {
  command = plan

  module {
    source = "./child"
  }

  variables {
    name = "supplied"
  }

  assert {
    condition     = output.echo == "supplied"
    error_message = "the child module echoes its input"
  }
}
