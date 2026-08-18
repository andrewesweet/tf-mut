# The same pair, declared the other way round: attribution must not depend on
# which assertion Terraform evaluated first.
run "applied" {
  command = apply

  assert {
    condition     = output.twin == "kept"
    error_message = "second assertion"
  }

  assert {
    condition     = output.app == "kept"
    error_message = "first assertion"
  }
}
