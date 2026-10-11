terraform {
  required_version = ">= 1.6"

  backend "azurerm" {}
}

resource "terraform_data" "r" {
  input = "kept"
}

resource "terraform_data" "checksum" {
  input = filesha256("${path.module}/main.tf")
}
