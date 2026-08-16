# Constructs with no plan or state projection. Nothing an assertion can read
# distinguishes their mutants, and reporting them as ordinary survivors would be
# crying wolf — so they are StructurallyUnassertable, in the denominator, with a
# negative test named as the fix.

variable "size" {
  type    = number
  default = 2

  validation {
    condition     = var.size > 0
    error_message = "size must be positive"
  }
}

resource "terraform_data" "first" {
  input = "one"
}

resource "terraform_data" "second" {
  input = "two"

  depends_on = [terraform_data.first]

  lifecycle {
    precondition {
      condition     = var.size > 0
      error_message = "size must be positive"
    }
  }
}

output "both" {
  value = "${terraform_data.first.input}-${terraform_data.second.input}"
}
