run "applied" {
  command = apply

  assert {
    condition     = output.app == "kept"
    error_message = "first assertion"
  }

  assert {
    condition     = output.twin == "kept"
    error_message = "second assertion"
  }
}
