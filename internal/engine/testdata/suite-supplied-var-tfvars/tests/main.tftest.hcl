run "setter" {
  command = plan

  variables {
    instance = "supplied"
  }

  assert {
    condition     = output.echo == "supplied"
    error_message = "the echo must carry the supplied instance"
  }
}
