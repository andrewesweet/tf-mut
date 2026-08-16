terraform {
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}

resource "null_resource" "app" {
  triggers = {
    env = "dev"
  }
}

output "env" {
  value = null_resource.app.triggers.env
}
