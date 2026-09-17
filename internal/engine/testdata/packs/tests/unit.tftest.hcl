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
}
