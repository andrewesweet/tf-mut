resource "terraform_data" "cloud_checksum" {
  input = filesha256("${path.module}/cloud_override.tf")
}

resource "terraform_data" "backend_checksum" {
  input = filesha256("${path.module}/state_override.tf")
}
