run "all_off" {
  command = plan

  variables {
    enabled = false
    items   = {}
  }

  assert {
    condition     = length(terraform_data.gated) == 0
    error_message = "the gated resource must not be instantiated"
  }

  assert {
    condition     = length(terraform_data.each_gated) == 0
    error_message = "the each_gated resource must not be instantiated"
  }

  assert {
    condition     = terraform_data.anchor.input == "steady"
    error_message = "the anchor must be planned"
  }
}
