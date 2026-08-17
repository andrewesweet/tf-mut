# The #44 review's reproduction shape: var.region is consumed only by
# provider configuration. The graph does not model provider influence edge by
# edge, so the variable's cone must be unbounded — observable, everything
# in-cone — and no static shortcut may turn the missing edges into a proof.
# Discovery-only: this fixture is parsed, never executed offline.

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

variable "region" {
  type    = string
  default = "eu-west-2"
}

provider "aws" {
  region = var.region
}

resource "aws_sqs_queue" "work" {
  name = "provider-config-probe"
}

output "queue" {
  value = aws_sqs_queue.work.name
}
