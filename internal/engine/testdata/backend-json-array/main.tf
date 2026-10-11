resource "terraform_data" "r" {
  input = "kept"
}

resource "terraform_data" "checksum" {
  input = filesha256("${path.module}/backend.tf.json")
}
