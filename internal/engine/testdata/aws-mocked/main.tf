terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type    = string
  default = "eu-west-2"
}

variable "name" {
  type    = string
  default = "tf-mut"
}

variable "environment" {
  type    = string
  default = "dev"
}

variable "retention_days" {
  type    = number
  default = 14
}

locals {
  is_production = var.environment == "prod"
  state_attributes = [
    { name = "id", type = "S" },
  ]
  bucket_name = "${var.name}-${var.environment}-artifacts"
  tags = {
    Name        = var.name
    Environment = var.environment
    Tier        = local.is_production ? "critical" : "standard"
  }
}

output "bucket" {
  value = aws_s3_bucket.artifacts.bucket
}

output "queue_name" {
  value = aws_sqs_queue.work.name
}

output "tier" {
  value = local.tags.Tier
}

output "retention" {
  value = aws_cloudwatch_log_group.application.retention_in_days
}
