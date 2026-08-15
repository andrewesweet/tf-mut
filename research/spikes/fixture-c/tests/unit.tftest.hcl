run "root_wires_module" {
  command = plan
  assert {
    condition     = length(output.subnets) == 2
    error_message = "two subnets"
  }
  assert {
    condition     = output.subnets[0] == "10.0.0.0/24"
    error_message = "first subnet wrong"
  }
}

run "module_directly" {
  command = plan
  module {
    source = "./modules/net"
  }
  variables {
    cidr = "192.168.0.0/16"
    azs  = ["a", "b", "c"]
  }
  assert {
    condition     = output.count == 3
    error_message = "three subnets"
  }
}
