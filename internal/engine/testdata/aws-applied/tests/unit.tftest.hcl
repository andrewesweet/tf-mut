mock_provider "aws" {}

run "applied" {
  command = apply

  assert {
    condition     = output.queue == "tf-mut-applied-work"
    error_message = "the queue keeps its configured name"
  }

  assert {
    condition     = output.label == "Work"
    error_message = "the label is title-cased"
  }
}
