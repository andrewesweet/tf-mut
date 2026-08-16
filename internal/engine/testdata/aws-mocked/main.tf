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
  bucket_name   = "${var.name}-${var.environment}-artifacts"
  tags = {
    Name        = var.name
    Environment = var.environment
    Tier        = local.is_production ? "critical" : "standard"
  }
}

resource "aws_s3_bucket" "artifacts" {
  bucket        = local.bucket_name
  force_destroy = !local.is_production
  tags          = local.tags
}

resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id

  versioning_configuration {
    status = local.is_production ? "Enabled" : "Suspended"
  }
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_sqs_queue" "work" {
  name                       = "${var.name}-${var.environment}-work"
  visibility_timeout_seconds = local.is_production ? 300 : 30
  message_retention_seconds  = var.retention_days * 86400
  tags                       = local.tags
}

resource "aws_sqs_queue" "dead_letter" {
  name                      = "${var.name}-${var.environment}-dlq"
  message_retention_seconds = 1209600
  tags                      = local.tags
}

resource "aws_sns_topic" "alerts" {
  name = "${var.name}-${var.environment}-alerts"
  tags = local.tags
}

resource "aws_cloudwatch_log_group" "application" {
  name              = "/aws/${var.name}/${var.environment}"
  retention_in_days = var.retention_days
  tags              = local.tags
}

resource "aws_iam_role" "task" {
  name = "${var.name}-${var.environment}-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy" "task" {
  name = "${var.name}-${var.environment}-task"
  role = aws_iam_role.task.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject", "s3:PutObject"]
      Resource = "${aws_s3_bucket.artifacts.arn}/*"
    }]
  })
}

resource "aws_dynamodb_table" "state" {
  name         = "${var.name}-${var.environment}-state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  tags = local.tags
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
