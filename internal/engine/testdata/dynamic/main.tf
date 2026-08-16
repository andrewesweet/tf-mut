# DYNAMIC-ZERO's generation site.
#
# A dynamic block needs a provider whose schema declares a nested block type,
# and neither `terraform_data` nor `null_resource` has one. The block below is
# therefore accepted by discovery and rejected by Terraform's own validation, so
# this fixture is previewed rather than run: it backs the operator's generation
# site and mutation text, and its end-to-end classification waits for the
# real-provider gate.

variable "names" {
  type    = list(string)
  default = ["alpha", "beta"]
}

resource "terraform_data" "generated" {
  input = "static"

  dynamic "setting" {
    for_each = var.names

    content {
      value = setting.value
    }
  }
}

output "generated" {
  value = terraform_data.generated.input
}
