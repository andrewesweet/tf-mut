run "planned" {
  command = plan

  assert {
    condition     = length(terraform_data.gated) == 0
    error_message = "the gated resource must not be instantiated"
  }
}
