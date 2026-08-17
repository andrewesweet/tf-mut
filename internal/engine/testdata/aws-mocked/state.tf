# The dynamic block is the DYNAMIC-ZERO generation site: the aws provider's
# schema declares `attribute` as a nested block type, which neither offline
# provider does, so this is where the operator's end-to-end classification
# lives (M3c, issue #50).
resource "aws_dynamodb_table" "state" {
  name         = "${var.name}-${var.environment}-state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  dynamic "attribute" {
    for_each = local.state_attributes

    content {
      name = attribute.value.name
      type = attribute.value.type
    }
  }

  tags = local.tags
}
