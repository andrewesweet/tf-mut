run "auto_loaded" {
  command = plan

  assert {
    condition     = length(terraform_data.gated) == 1
    error_message = "the auto-loaded variable file must instantiate the block"
  }
}
