terraform {
  required_providers {
    null = { source = "hashicorp/null", version = "~> 3.2" }
  }
}

variable "env" { default = "dev" }
variable "enable_backup" { default = true }

locals {
  backup_count = var.enable_backup && var.env == "prod" ? 2 : 1
}

resource "null_resource" "app" {
  triggers = {
    env  = var.env
    tier = var.env == "prod" ? "critical" : "standard"
  }
}

resource "null_resource" "backup" {
  count = var.enable_backup ? local.backup_count : 0
  triggers = {
    source = null_resource.app.id
  }
}

output "app_id" { value = null_resource.app.id }
output "backup_count" { value = length(null_resource.backup) }
