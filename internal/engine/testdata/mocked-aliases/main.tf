# PROVIDER-ALIAS-SWAP may only move a resource between provider configurations
# of identical mock status. A swap that routed execution to an unmocked provider
# would bypass a safety gate that has already run.

terraform {
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}

provider "null" {
  alias = "primary"
}

provider "null" {
  alias = "secondary"
}

resource "null_resource" "placed" {
  provider = null.primary

  triggers = {
    region = "primary"
  }
}

output "placed" {
  value = null_resource.placed.triggers.region
}
