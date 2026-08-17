# The M3a.2 static-shortcut control: `local.unused` reaches nothing — no
# resource, no output, no check, no contract construct — so the shortcut may
# classify its mutants `Unobservable` without an execution, and the control
# run with the shortcut disabled must reach the identical verdict the long
# way. The suite runs in apply mode so the executed verdict is `Unobservable`
# rather than an unknown-values indeterminacy.

resource "terraform_data" "app" {
  input = "constant"
}

locals {
  unused = "nobody-reads-me"
}

output "echo" {
  value = terraform_data.app.input
}
