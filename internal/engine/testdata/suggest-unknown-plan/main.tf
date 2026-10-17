# The known-to-unknown plan fixture: an output whose value a conditional
# routes between a known literal and a computed attribute. Under a
# `command = plan` run, a mutation that forces the other branch leaves the
# output unknown at plan, so a plan assertion over it errors with
# "Unknown condition value" when the suite replays the suggestion.
resource "terraform_data" "flag" {
  input = true
}

resource "terraform_data" "other" {
  input = "deep"
}

output "echo" {
  value = terraform_data.flag.input ? "on" : terraform_data.other.output
}

output "nested" {
  value = {
    computed = terraform_data.flag.input ? "on" : terraform_data.other.output
    steady   = "fixed"
  }
}

output "plain" {
  value = "steady"
}
