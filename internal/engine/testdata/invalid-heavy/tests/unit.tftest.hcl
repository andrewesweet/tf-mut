run "planned" {
  command = plan

  assert {
    condition     = module.child.joined == "one-two-three-four"
    error_message = "all child inputs must contribute to the joined output"
  }
}
