# Issue #57's second reproduction: `local.json_only` is read by a JSON-declared
# resource and by nothing else, so the `.tf`-only reference graph gives it an
# empty forward cone. The static `Unobservable` shortcut would then prove
# unobservability of a value the suite demonstrably observes — a false proof of
# exactly the shape the M3 spec review's C3 prohibited.

resource "terraform_data" "anchor" {
  input = "steady"
}

locals {
  json_only = "read-from-json"
}

output "anchor" {
  value = terraform_data.anchor.input
}
