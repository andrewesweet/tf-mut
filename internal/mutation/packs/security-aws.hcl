// The shipped `security-aws` pack (M5c.2). These are exactly the ten
// entries the M5-0.3 seed census and admission measurement enabled
// (docs/research/18-m5-pack-seed-census.md, "Per-entry result") plus the one
// scalar candidate the census deferred to its dedicated form: #176 implemented
// PACK-WIDEN-CIDR and admitted `trivy-aws-0104` by its own measurement. Every
// entry produced bytes on the measured corpus with a zero invalid rate, and
// the 105 remaining loadable candidates that produced no bytes stay published
// as unwitnessed, not enabled. Each entry carries its upstream rule identifier
// and the licence of the catalogue it was derived from.

// Checkov CKV_AWS_7: KMS key rotation disabled.
entry "checkov-aws-7" {
  resource_type  = "aws_kms_key"
  attribute      = "enable_key_rotation"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "CKV_AWS_7"
  source_licence = "Apache-2.0"
}

// Checkov CKV_AWS_53–56: the four aws_s3_bucket_public_access_block flags,
// each one a public-access guard switched off.
entry "checkov-aws-53" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "block_public_acls"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "CKV_AWS_53"
  source_licence = "Apache-2.0"
}

entry "checkov-aws-54" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "block_public_policy"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "CKV_AWS_54"
  source_licence = "Apache-2.0"
}

entry "checkov-aws-55" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "ignore_public_acls"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "CKV_AWS_55"
  source_licence = "Apache-2.0"
}

entry "checkov-aws-56" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "restrict_public_buckets"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "CKV_AWS_56"
  source_licence = "Apache-2.0"
}

// Trivy AWS-0065: the same KMS rotation fault under the second catalogue.
// Both entries are kept: two entries of one pack requesting identical bytes
// both become origins of the surviving row.
entry "trivy-aws-0065" {
  resource_type  = "aws_kms_key"
  attribute      = "enable_key_rotation"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "AWS-0065"
  source_licence = "MIT"
}

// Trivy AWS-0086, AWS-0087, AWS-0091 and AWS-0093: the same four flags under
// the second catalogue.
entry "trivy-aws-0086" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "block_public_acls"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "AWS-0086"
  source_licence = "MIT"
}

entry "trivy-aws-0087" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "block_public_policy"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "AWS-0087"
  source_licence = "MIT"
}

entry "trivy-aws-0091" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "ignore_public_acls"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "AWS-0091"
  source_licence = "MIT"
}

entry "trivy-aws-0093" {
  resource_type  = "aws_s3_bucket_public_access_block"
  attribute      = "restrict_public_buckets"
  form           = "flip"
  from           = true
  to             = false
  source_rule    = "AWS-0093"
  source_licence = "MIT"
}

// Trivy AWS-0104: a security-group egress rule opened to the whole internet.
// The one scalar candidate the census deferred to #176's PACK-WIDEN-CIDR: the
// entry fires on any top-level scalar IPv4 CIDR narrower than the any-prefix
// — every `/0` prefix is a no-op — and widens it to `0.0.0.0/0`.
entry "trivy-aws-0104" {
  resource_type  = "aws_vpc_security_group_egress_rule"
  attribute      = "cidr_ipv4"
  form           = "widen-cidr"
  from           = "any-cidr"
  to             = "0.0.0.0/0"
  source_rule    = "AWS-0104"
  source_licence = "MIT"
}
