run "rejects_a_negative_size" {
  command = plan

  variables {
    size = -1
  }

  expect_failures = [var.size]
}

run "accepts_the_default" {
  command = plan

  assert {
    condition     = output.size == 2
    error_message = "the default size must survive"
  }
}
