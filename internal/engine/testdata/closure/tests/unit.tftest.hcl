run "applied" {
  command = apply

  # Reads the delta transitively, but far too loosely to catch a change to it.
  assert {
    condition     = output.summary != ""
    error_message = "the summary must not be empty"
  }

  assert {
    condition     = length(output.labels) == 2
    error_message = "both labels must be projected"
  }
}
