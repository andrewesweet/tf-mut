mock_provider "null" {}

mock_provider "null" {
  alias = "primary"
}

mock_provider "null" {
  alias = "secondary"
}

run "planned" {
  command = plan

  assert {
    condition     = output.placed == "primary"
    error_message = "the resource is placed on the primary configuration"
  }
}
