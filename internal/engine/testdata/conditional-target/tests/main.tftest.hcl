run "scoped" {
  command = plan

  plan_options {
    target = [terraform_data.anchor]
  }

  assert {
    condition     = terraform_data.anchor.input == "steady"
    error_message = "the anchor must be planned"
  }
}
