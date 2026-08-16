run "planned" {
  command = plan

  assert {
    condition     = terraform_data.source.input == "one"
    error_message = "the source input must carry the suffix"
  }
}
