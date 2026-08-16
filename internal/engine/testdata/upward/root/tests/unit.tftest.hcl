run "wires_the_shared_module" {
  command = plan

  assert {
    condition     = output.name == "root-network"
    error_message = "the upward module source must resolve"
  }
}
