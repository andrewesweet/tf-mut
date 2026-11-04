# The file the stopped invocation never reaches: failing here would only cost
# time after the verdict is already decided, and its absence from the record is
# the proof that fail-fast still ended the child.
run "after" {
  command = plan

  assert {
    condition     = output.watched == "kept"
    error_message = "the file after the parallel one must observe the watched value"
  }
}
