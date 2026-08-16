# The C3 reproduction: a delta observed only through a local and an output.
#
# The assertion never names terraform_data.graded, so address intersection alone
# would report "no assertion reads this". The forward closure through the local
# and the output has to find it, and diagnose a weak assertion instead.

locals {
  tier = "critical"
}

resource "terraform_data" "graded" {
  input = local.tier
}

locals {
  summary = terraform_data.graded.input
}

output "summary" {
  value = local.summary
}

# A splat projects one attribute of a collection. An assertion reading the
# projection proves the collection was read, not that this attribute was, so the
# closure is defeated and the honest answer is `unasserted`.
resource "terraform_data" "projected" {
  count = 2

  input = {
    label  = "visible"
    hidden = "unreadable"
  }
}

output "labels" {
  value = terraform_data.projected[*].input.label
}

# Nothing at all reads this one.
resource "terraform_data" "ignored" {
  input = "unwatched"
}
