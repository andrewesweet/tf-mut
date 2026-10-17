run "consumer" {
  command = plan

  variables {
    instance = "supplied"
  }

  assert {
    condition     = output.echo == "supplied"
    error_message = "the child must echo the supplied instance"
  }
}
