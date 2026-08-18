resource "terraform_data" "inner" {
  input = "side"

  provisioner "local-exec" {
    command = "touch $TF_MUT_MARKER"
  }
}
