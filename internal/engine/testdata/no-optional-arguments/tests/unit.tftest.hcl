run "defaults" {
  command = plan

  assert {
    condition     = terraform_data.bare.input == null
    error_message = "the bare resource configures nothing"
  }
}
