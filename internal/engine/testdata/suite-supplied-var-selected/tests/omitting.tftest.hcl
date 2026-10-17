run "omitter" {
  command = plan

  assert {
    condition     = output.echo == "builtin-default"
    error_message = "the echo must carry the declared default"
  }
}
