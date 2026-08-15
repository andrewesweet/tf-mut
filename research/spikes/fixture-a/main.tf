variable "env" {
  type    = string
  default = "dev"
  validation {
    condition     = contains(["dev", "stage", "prod"], var.env)
    error_message = "env must be dev, stage or prod"
  }
}

variable "replica_count" {
  type    = number
  default = 2
}

locals {
  is_prod  = var.env == "prod"
  replicas = local.is_prod ? max(var.replica_count, 3) : var.replica_count
  tags = {
    Environment = var.env
    Tier        = local.is_prod ? "critical" : "standard"
  }
}

resource "terraform_data" "node" {
  count = local.replicas
  input = {
    name = "${var.env}-node-${count.index}"
    tags = local.tags
  }
}

output "node_names" {
  value = [for n in terraform_data.node : n.input.name]
}

output "replica_count" {
  value = local.replicas
}

output "tier" {
  value = local.tags.Tier
}
