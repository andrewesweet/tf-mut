run "applied" {
  command = apply

  assert {
    condition     = output.watched == "watched"
    error_message = "the watched output must carry its input"
  }
}
