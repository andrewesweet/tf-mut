# The mock-masked refutation fixture (M3c, issue #50). The queue's
# kms_data_key_reuse_period_seconds is optional AND computed: deleting it
# makes the mock invent a value, and the resulting apply-mode delta is stable
# — yet it is attributable to the module, because the attribute is
# configurable and an assertion could pin it. The withdrawn diagnosis must
# never come back for this shape. Network-gated: hashicorp/aws is not in the
# offline mirror.

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = "eu-west-2"
}

resource "aws_sqs_queue" "work" {
  name                              = "tf-mut-applied-work"
  kms_data_key_reuse_period_seconds = 300
}

output "queue" {
  value = aws_sqs_queue.work.name
}
