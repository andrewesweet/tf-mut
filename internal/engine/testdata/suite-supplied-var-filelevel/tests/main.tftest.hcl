variables {
  instance = "from-file"
}

run "first" {
  command = plan

  assert {
    condition     = output.echo == "from-file"
    error_message = "the echo must carry the file-level instance"
  }
}

run "second" {
  command = plan

  assert {
    condition     = output.echo == "from-file"
    error_message = "the echo must carry the file-level instance"
  }
}
