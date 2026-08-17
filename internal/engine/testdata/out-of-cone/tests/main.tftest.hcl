run "planned" {
  command = plan

  assert {
    condition     = terraform_data.noise.input == "watched"
    error_message = "the noise resource must be planned"
  }

  assert {
    condition     = output.tier == "standard"
    error_message = "the tier must be standard"
  }
}
