# The M5a lifecycle fixture (offline, terraform_data-based): every admitted
# Tier 4 operator carries its site and the day-two run-block sequence the
# M5-0.1 measurement killed it with. Each pair owns one state_key so the three
# sequences stay independent.
#
#   held     ignore_changes = [input]              killed by LC-IGNORE-DROP
#   wide     ignore_changes = [triggers_replace]   killed by LC-IGNORE-ALL
#   replaced replace_triggered_by = [trigger]      killed by LC-REPLACE-TRIGGER-DROP

variable "value" {
  default = "old"
}

variable "revision" {
  default = "v1"
}

resource "terraform_data" "trigger" {
  input = var.revision
}

# The LC-IGNORE-DROP witness: the attribute is ignored, and the day-two plan
# passes a changed value that a dropped ignore must let through.
resource "terraform_data" "held" {
  input            = var.value
  triggers_replace = "fixed"

  lifecycle {
    ignore_changes = [input]
  }
}

# The LC-IGNORE-ALL witness: only the constant is ignored, so the day-two plan
# watches the input move — which the all keyword would hold.
resource "terraform_data" "wide" {
  input            = var.value
  triggers_replace = "fixed"

  lifecycle {
    ignore_changes = [triggers_replace]
  }
}

# The LC-REPLACE-TRIGGER-DROP witness: a second apply over the same state_key
# changes the trigger, and the id must move.
resource "terraform_data" "replaced" {
  input            = "old"
  triggers_replace = "fixed"

  lifecycle {
    replace_triggered_by = [terraform_data.trigger]
  }
}

output "replaced_id" {
  value = terraform_data.replaced.id
}
