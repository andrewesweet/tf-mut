run "first" {
  command = plan

  assert {
    condition     = output.watched == "kept"
    error_message = "the watched output must keep its value"
  }
}

run "second" {
  command = plan

  assert {
    condition     = output.watched == "kept"
    error_message = "the second run must observe the value"
  }
}

run "third" {
  command = plan

  assert {
    condition     = output.watched == "kept"
    error_message = "the third run must observe the value"
  }
}
