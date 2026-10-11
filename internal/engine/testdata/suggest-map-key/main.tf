terraform {
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}

# The suggestion engine's map-key module: one map attribute whose keys include
# a dotted Kubernetes label key. The payload flattens that key across several
# dot-separated path segments, and only the provider schema's map type can tell
# the key from a nested attribute — the shape the rendering walk disambiguates
# and renders in index form.
resource "null_resource" "app" {
  triggers = {
    "app.kubernetes.io/part-of" = "tf-mut-observe"
    plain                       = "value"
  }
}
