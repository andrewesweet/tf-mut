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
