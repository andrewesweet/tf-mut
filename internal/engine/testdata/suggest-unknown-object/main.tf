terraform {
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}

# The unknown-object module: an object output one of whose members is an
# unknown attribute value. Under a plan run, the baseline payload marks that
# member in the output's after_unknown record while the value itself is an
# object with members — exactly the shape a length assertion reads as an
# empty collection and answers with a lie about the baseline.
resource "null_resource" "app" {
  triggers = {
    ready = "steady"
  }
}

output "identity" {
  value = {
    principal_id = null_resource.app.id
    tenant       = "steady-tenant"
  }
}
