# Two target files with a candidate each, so the apply protocol has more than
# one file to write and a failure between them has a partial state to report.
# The root module's resources reach only the root-targeted run, and the child's
# reach only the run that retargets it, so each run carries a delta of its own.

resource "terraform_data" "root_only" {
  input = "root-value"
}

output "root_only" {
  value = terraform_data.root_only.input
}
