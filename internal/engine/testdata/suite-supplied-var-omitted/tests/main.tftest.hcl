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

run "omitter" {
  command = plan

  assert {
    condition     = output.echo == "builtin-default"
    error_message = "the echo must carry the declared default"
  }
}
