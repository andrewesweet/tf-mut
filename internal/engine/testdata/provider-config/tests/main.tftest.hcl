mock_provider "aws" {}

run "planned" {
  command = plan

  assert {
    condition     = aws_sqs_queue.work.name == "provider-config-probe"
    error_message = "the queue must be planned"
  }
}
