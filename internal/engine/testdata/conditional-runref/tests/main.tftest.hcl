run "setup" {
  command = plan

  assert {
    condition     = output.gate == false
    error_message = "the gate must be false"
  }
}

run "off" {
  command = plan

  variables {
    enabled = run.setup.gate
  }

  assert {
    condition     = length(terraform_data.gated) == 0
    error_message = "the gated resource must not be instantiated"
  }
}
