# A terraform block carrying nested content this version does not model must
# leave the file unread: required_version is deliberately accepted, and
# everything else in the block is refused rather than silently dropped.

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.input
}
