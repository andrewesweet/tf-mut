terraform {
  backend "azurerm" {}
}

resource "terraform_data" "r" {
  input = "kept"
}
