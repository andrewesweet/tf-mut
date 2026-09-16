variable "revision" {
  default = "v1"
}

resource "terraform_data" "trigger" {
  input = var.revision
}

resource "terraform_data" "subject" {
  input            = "old"
  triggers_replace = "fixed"

  lifecycle {
    replace_triggered_by = [terraform_data.trigger]
  }
}

output "subject_id" {
  value = terraform_data.subject.id
}
