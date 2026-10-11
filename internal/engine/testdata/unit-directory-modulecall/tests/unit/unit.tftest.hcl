run "calls_child" {
  command = plan

  module {
    source = "./child"
  }

  assert {
    condition     = output.out == "child"
    error_message = "child output mismatch"
  }
}
