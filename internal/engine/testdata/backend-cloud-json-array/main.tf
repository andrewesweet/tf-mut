resource "terraform_data" "cloud_checksum" {
  input = filesha256("${path.module}/cloud.tf.json")
}

resource "terraform_data" "backend_checksum" {
  input = filesha256("${path.module}/state_override.tf")
}
