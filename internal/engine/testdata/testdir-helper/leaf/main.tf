resource "terraform_data" "marker" {
  input = "ready"
}

output "ready" {
  value = terraform_data.marker.input
}
