run "plans" {
  command = plan

  assert {
    condition     = terraform_data.r.input == "kept"
    error_message = "input mismatch"
  }
}
