terraform {
  required_version = ">= 1.6"
}

resource "terraform_data" "r" {
  input = "kept"
}
