run "applied" {
  command = apply

  assert {
    condition     = output.echo == "constant"
    error_message = "the echo must carry the input"
  }
}
