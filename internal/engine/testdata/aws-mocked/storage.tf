# M5-0.3: the enabled security-aws seed entries for KMS key rotation share
# this literal witness. The pack provenance keeps both source rules visible
# after their identical bytes are deduplicated.
resource "aws_kms_key" "artifacts" {
  description         = "Encrypt the tf-mut fixture artifacts"
  enable_key_rotation = true
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

# M5c.2/#176: the shipped widen-cidr entry's witness. The egress CIDR is
# deliberately unasserted, like the KMS rotation flag above: the pack mutant
# must survive plan so the suggestion engine can synthesise the assertion
# that kills it.
resource "aws_security_group" "egress" {
  name        = "${local.bucket_name}-egress"
  description = "Egress for the tf-mut fixture artifacts"
}

resource "aws_vpc_security_group_egress_rule" "artifacts" {
  cidr_ipv4         = "10.0.0.0/8"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  security_group_id = aws_security_group.egress.id
}
