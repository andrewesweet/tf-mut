locals {
  region = "westus3"
}

resource "terraform_data" "marker" {
  input = local.region
}

output "ready" {
  value = local.region == "westus3" ? "ready" : "not-ready"
}
