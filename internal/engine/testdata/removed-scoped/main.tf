# Issue #70's twin: a `removed` block carries a destroy-time provisioner, which
# is an effect, and names the resource whose provider would run the destroy.

resource "terraform_data" "anchor" {
  input = "steady"
}

removed {
  from = null_resource.gone

  lifecycle {
    destroy = true
  }

  provisioner "local-exec" {
    when    = destroy
    command = "touch $TF_MUT_MARKER"
  }
}

output "anchor" {
  value = terraform_data.anchor.input
}
