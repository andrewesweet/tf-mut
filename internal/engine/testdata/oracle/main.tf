variable "env" {
  type    = string
  default = "dev"
}

locals {
  # Nothing reads this: mutating it can change no plan and no state.
  unread = "spare"
}

resource "terraform_data" "app" {
  input = {
    tier = var.env == "prod" ? "critical" : "standard"
  }
}

output "tier" {
  value = terraform_data.app.input.tier
}
