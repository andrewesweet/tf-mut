variables {
  secret = "s3cret-value"
}

run "applied" {
  command = apply

  # Deliberately no assertion on output.tier: the collection mutants must
  # survive so their deltas — where the secret surfaces — reach the report.
  assert {
    condition     = output.anchor == "steady"
    error_message = "the anchor must be applied"
  }
}
