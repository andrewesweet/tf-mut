run "unit" {
  command = plan

  variables {
    instance = "suite-supplied"
  }

  plan_options {
    target = [terraform_data.app]
  }

  assert {
    condition     = length(output.instance) > 0
    error_message = "instance must be set"
  }
}
