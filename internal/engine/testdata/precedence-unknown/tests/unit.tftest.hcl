run "planned" {
  command = plan

  assert {
    condition     = terraform_data.seed.input == "seed"
    error_message = "the seed must be planned"
  }
}
