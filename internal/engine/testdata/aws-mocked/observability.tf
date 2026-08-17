resource "aws_cloudwatch_log_group" "application" {
  name              = "/aws/${var.name}/${var.environment}"
  retention_in_days = var.retention_days
  tags              = local.tags
}
