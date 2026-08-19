# A reusable module: it names the provider configurations its caller must pass
# in with `configuration_aliases` and declares no `provider` block at all. A
# reader that only collects `provider` blocks sees no alias here, mocks none of
# them, and the gate that requires a mock per provider configuration never
# learns the configurations exist.

terraform {
  required_providers {
    null = {
      source                = "hashicorp/null"
      version               = "3.2.4"
      configuration_aliases = [null.primary, null.secondary]
    }
  }
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
