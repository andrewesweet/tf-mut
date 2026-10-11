run "applied" {
  command = apply

  assert {
    condition     = output.checked == "checked"
    error_message = "the checked output must carry its input"
  }
}
