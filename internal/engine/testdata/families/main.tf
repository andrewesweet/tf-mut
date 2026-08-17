# The M3e fixture (#53): one call per semantic family, the core:: alias form,
# and the cross-family bait the C7 review named — file() shares the unary
# string-to-string signature with upper(), and no generated substitution may
# ever connect them.

locals {
  chosen  = min(1, 2)
  aliased = core::max(3, 4)
  rounded = floor(1.5)
  label   = title("name")
  found   = startswith("abc", "a")
  merged  = setunion(["a"], ["b"])
  bait    = file("${path.module}/data.txt")
}

resource "terraform_data" "app" {
  input = {
    chosen  = local.chosen
    aliased = local.aliased
    rounded = local.rounded
    label   = local.label
    found   = local.found
    merged  = local.merged
    bait    = local.bait
  }
}

output "echo" {
  value = terraform_data.app.input
}
