# The M3a.2 out-of-cone fixture: the payload always carries unknowns in plan
# mode — `noise` contributes id and output — but none of them lie in the
# forward cone of a mutation inside `output.tier`, whose cone reaches no
# resource at all. Under the path-scoped rule those unknowns no longer block
# the equality claim, and `Unobservable` fires in plan mode for the first
# time.

resource "terraform_data" "noise" {
  input = "watched"
}

locals {
  tier = "standard"
}

# The conditional is genuinely redundant: both branches are the same value, so
# no mutation of the condition or the branch order can move the payload.
output "tier" {
  value = local.tier == "" ? "standard" : "standard"
}

output "noise" {
  value = terraform_data.noise.input
}
