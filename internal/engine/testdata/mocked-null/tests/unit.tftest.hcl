mock_provider "null" {}

run "defaults" {
  command = plan

  assert {
    condition     = output.env == "dev"
    error_message = "env trigger must be dev"
  }
}
