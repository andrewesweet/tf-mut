run "applied" {
  command = apply

  # Nothing asserts on the token, so a mutation of its stable suffix has to
  # survive and carry that suffix as evidence. If the mask erased the suffix the
  # mutant would be excluded as unobservable and the finding would vanish.
  assert {
    condition     = output.derived == uuidv5("dns", "example.com")
    error_message = "the derived identifier must be deterministic"
  }
}
