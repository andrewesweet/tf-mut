terraform {
  cloud {}
}

resource "terraform_data" "cloud_checksum" {
  input = filesha256("${path.module}/main.tf")
}

resource "terraform_data" "backend_checksum" {
  input = filesha256("${path.module}/override.tf.json")
}
