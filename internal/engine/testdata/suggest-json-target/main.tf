# The unsupported-target case: the only run block lives in a `.tftest.json`
# file, so every survivor's delta is carried by a run this tool has no writer
# for. The suggestion is reported with its status and no patch, and `--apply`
# never touches the file.

resource "terraform_data" "ignored" {
  input = "nobody-checks-me"
}

resource "terraform_data" "asserted" {
  input = "asserted"
}

output "ignored" {
  value = terraform_data.ignored.input
}

output "asserted" {
  value = terraform_data.asserted.input
}
