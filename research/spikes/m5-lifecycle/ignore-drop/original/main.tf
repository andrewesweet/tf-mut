variable "value" {
  default = "old"
}
variable "revision" {
  default = "v1"
}

resource "terraform_data" "trigger" {
  input = var.revision
}

resource "terraform_data" "subject" {
  input            = var.value
  triggers_replace = "fixed"

  lifecycle {
    ignore_changes = [input]
  }
}

output "subject_id" {
  value = terraform_data.subject.id
}
