# The contract story's other direction: a validation rule a run block actually
# exercises. Weakening or removing it has to be caught.

variable "size" {
  type    = number
  default = 2

  validation {
    condition     = var.size > 0
    error_message = "size must be positive"
  }
}

resource "terraform_data" "app" {
  input = var.size
}

output "size" {
  value = terraform_data.app.input
}
