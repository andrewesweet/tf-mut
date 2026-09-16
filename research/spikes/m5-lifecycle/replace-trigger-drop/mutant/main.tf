variable "revision" {
  default = "v1"
}

resource "terraform_data" "trigger" {
  input = var.revision
}

resource "terraform_data" "subject" {
  input            = "old"
  triggers_replace = "fixed"

  lifecycle {}
}

output "subject_id" {
  value = terraform_data.subject.id
}