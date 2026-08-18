# The sensitivity matrix, all three cases in one module: a sensitive value
# reached through a local, a nested sensitive object member, and a sensitive
# output. Terraform marks each of them in the payload's sensitivity mirror, and
# no suggestion artefact may carry any of their values.
#
# The secret is supplied by the suite rather than defaulted here on purpose: a
# literal in the module source appears in every mutant diff, which would test
# the fixture's own carelessness rather than the sensitivity predicate.

variable "secret" {
  type      = string
  sensitive = true
}

locals {
  through_local = var.secret
}

resource "terraform_data" "app" {
  input = {
    plain  = "visible"
    hidden = local.through_local
  }
}

resource "terraform_data" "direct" {
  input = var.secret
}

output "hidden" {
  value     = local.through_local
  sensitive = true
}

output "plain" {
  value = "visible"
}
