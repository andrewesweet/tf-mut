mock_provider "null" {
  mock_resource "null_resource" {
    defaults = {
      id = "mock-null-id"
    }
  }
}

run "dev_defaults" {
  command = plan
  assert {
    condition     = length(null_resource.backup) == 1
    error_message = "dev gets 1 backup"
  }
  assert {
    condition     = null_resource.app.triggers.tier == "standard"
    error_message = "dev tier standard"
  }
}

run "prod" {
  command = plan
  variables {
    env = "prod"
  }
  assert {
    condition     = length(null_resource.backup) == 2
    error_message = "prod gets 2 backups"
  }
}

run "no_backup" {
  command = plan
  variables {
    enable_backup = false
  }
  assert {
    condition     = length(null_resource.backup) == 0
    error_message = "disabled means zero"
  }
}

run "apply_with_mocks" {
  command = apply
  assert {
    condition     = output.app_id == "mock-null-id"
    error_message = "mocked id surfaces in apply"
  }
}
