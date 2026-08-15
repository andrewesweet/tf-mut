run "defaults" {
  command = plan
  assert {
    condition     = length(terraform_data.node) == 2
    error_message = "expected 2 nodes by default"
  }
  assert {
    condition     = output.tier == "standard"
    error_message = "dev should be standard tier"
  }
}

run "prod_floor" {
  command = plan
  variables {
    env           = "prod"
    replica_count = 1
  }
  assert {
    condition     = output.replica_count == 3
    error_message = "prod floors replicas at 3"
  }
  assert {
    condition     = output.tier == "critical"
    error_message = "prod is critical"
  }
}

run "bad_env" {
  command = plan
  variables {
    env = "qa"
  }
  expect_failures = [var.env]
}
