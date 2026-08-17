run "pinned_off" {
  command = plan

  assert {
    condition     = length(terraform_data.gated) == 0
    error_message = "terraform.tfvars must keep the block uninstantiated"
  }

  assert {
    condition     = terraform_data.anchor.input == "steady"
    error_message = "the anchor must be planned"
  }
}
