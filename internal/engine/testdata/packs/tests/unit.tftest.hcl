run "applied" {
  command = apply

  assert {
    condition     = output.flag == true
    error_message = "the flag must stay set"
  }

  assert {
    condition     = output.acl == "private"
    error_message = "the acl must stay private"
  }

  assert {
    condition     = output.size == 3
    error_message = "the size must stay three"
  }

  assert {
    condition     = output.cidr == "10.0.0.0/8"
    error_message = "the cidr must stay scoped"
  }
}
