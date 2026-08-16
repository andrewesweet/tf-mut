run "planned" {
  command = plan

  assert {
    condition     = output.generated == "static"
    error_message = "the static input must survive"
  }
}
