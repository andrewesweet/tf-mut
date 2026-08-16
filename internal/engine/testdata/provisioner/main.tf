resource "terraform_data" "app" {
  input = "applied"

  provisioner "local-exec" {
    command = "touch $TF_MUT_MARKER"
  }
}

output "app" {
  value = terraform_data.app.input
}
