run "applied" {
  command = apply

  variables {
    secret = "steady"
  }

  assert {
    condition     = output.secret == "steady"
    error_message = "the anchor must carry the secret"
  }
}
