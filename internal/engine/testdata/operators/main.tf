# The applicability matrix's fixture. Every enabled operator has a generation
# site here, so that a row in docs/design/mutation-operators.md which describes
# an operator nothing can fire on is a test failure rather than a plausible
# paragraph.

variable "environment" {
  type    = string
  default = "dev"

  validation {
    condition     = contains(["dev", "prod"], var.environment)
    error_message = "environment must be dev or prod"
  }
}

variable "replicas" {
  type    = number
  default = 2
}

variable "enabled" {
  type    = bool
  default = true
}

variable "names" {
  type    = list(string)
  default = ["alpha", "beta"]
}

variable "secret" {
  type      = string
  default   = "hunter2"
  sensitive = true
}

variable "strict" {
  type     = bool
  default  = false
  nullable = false
}

variable "shaped" {
  type = object({
    label = optional(string, "unset")
  })
  default = {}
}

locals {
  # Conditionals, comparison, boolean and numeric literals. The second
  # conditional's predicate is not a comparison, so COND-NEGATE has a site the
  # boolean operators do not also reach and deduplicate away.
  tier     = var.environment == "prod" ? "critical" : "standard"
  mode     = var.enabled ? "live" : "paused"
  scaled   = var.replicas > 1 && var.enabled ? var.replicas + 1 : 1
  either   = var.enabled || var.strict
  inverted = !var.enabled
  spare    = var.replicas >= 2
  flag     = true
  label    = "Steady"
  ratio    = 3 * 2

  # Collections.
  ordered = ["one", "two", "three"]
  tags = {
    owner = "platform"
    cost  = "engineering"
  }

  # For expressions and traversals.
  upper     = [for name in var.names : upper(name) if name != ""]
  selected  = [for name in var.names : name if var.enabled]
  keyed     = { for name in var.names : name => upper(name) }
  grouped   = { for name in var.names : substr(name, 0, 1) => name... }
  first     = var.names[0]
  projected = terraform_data.counted[*].input

  # Templates.
  greeting = "hello-${var.environment}-world"
  trimmed  = "start ${~var.environment} end"
  branched = "%{if var.enabled}on%{else}off%{endif}"
  document = <<-EOT
    environment = ${var.environment}
  EOT

  # The curated function list.
  bounded    = min(var.replicas, 10)
  fallback   = try(var.environment, "unset")
  guarded    = can(var.environment)
  normalised = distinct(var.names)
  joined     = join(",", var.names)
  merged     = merge(local.tags, { extra = "value" })
  expanded   = concat(var.names, [["gamma"]]...)
  looked     = lookup(local.tags, "owner", "nobody")
}

resource "terraform_data" "counted" {
  count = 2

  input = local.tier
}

resource "terraform_data" "keyed" {
  for_each = toset(var.names)

  input = each.value
}

# A resource whose instance set no consumer reads, so COUNT-ONE and
# COUNT-OFF-BY-ONE have a site of their own. DYNAMIC-ZERO's site lives in the
# `dynamic` fixture instead: a dynamic block needs a provider with a nested
# block type, and neither offline provider has one.
resource "terraform_data" "sized" {
  count = 3

  input = local.mode
}

resource "terraform_data" "ordered" {
  input = local.ordered

  depends_on = [terraform_data.counted]

  lifecycle {
    precondition {
      condition     = var.replicas > 0
      error_message = "replicas must be positive"
    }
  }
}

module "child" {
  source = "./child"

  needed = local.label
}

check "健康" {
  assert {
    condition     = var.replicas > 0
    error_message = "replicas must be positive"
  }
}

output "tier" {
  value = local.tier
}

output "secret" {
  value     = var.secret
  sensitive = true
}

# `sensitive = true` over a value nothing marks sensitive. OUT-SENSITIVE-FLIP
# fires here, and is skipped on `output.secret` above, where Terraform would
# refuse the non-sensitive output outright.
output "guarded" {
  value     = local.label
  sensitive = true
}
