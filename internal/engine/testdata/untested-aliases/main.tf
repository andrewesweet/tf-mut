# The module characterisation exists for: real providers, aliased
# configurations, and no test file anywhere. The unscaffolded module is not the
# program that would execute, so judging the safety gates against it would
# refuse exactly this case.

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

variable "env" {
  type    = string
  default = "dev"
}

resource "null_resource" "primary" {
  provider = null.primary

  triggers = {
    env = var.env
  }
}

resource "null_resource" "secondary" {
  provider = null.secondary

  triggers = {
    source = null_resource.primary.id
  }
}

resource "terraform_data" "anchor" {
  input = "steady-${var.env}"
}

output "anchor" {
  value = terraform_data.anchor.output
}

output "primary_id" {
  value = null_resource.primary.id
}
