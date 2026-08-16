mock_provider "aws" {}

run "development_defaults" {
  command = plan

  assert {
    condition     = output.tier == "standard"
    error_message = "development workloads are standard tier"
  }

  assert {
    condition     = output.bucket == "tf-mut-dev-artifacts"
    error_message = "the bucket name is derived from name and environment"
  }

  assert {
    condition     = output.retention == 14
    error_message = "the default retention is fourteen days"
  }
}

run "production" {
  command = plan

  variables {
    environment = "prod"
  }

  assert {
    condition     = output.tier == "critical"
    error_message = "production workloads are critical tier"
  }

  assert {
    condition     = aws_s3_bucket.artifacts.force_destroy == false
    error_message = "production buckets are never force-destroyed"
  }
}

run "queue_sizing" {
  command = plan

  assert {
    condition     = aws_sqs_queue.work.visibility_timeout_seconds == 30
    error_message = "development queues use the short visibility timeout"
  }
}
