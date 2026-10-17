run "planned" {
  command = plan

  assert {
    condition     = output.echo == "EDGE"
    error_message = "the child output must reach the assertion"
  }
}
