# Redaction applies to every artefact the pipeline emits. The value below must
# reach no generated file, no report field and no terminal line.

variable "token" {
  type      = string
  default   = "tfmut-characterise-secret"
  sensitive = true
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "token" {
  value     = var.token
  sensitive = true
}

output "anchor" {
  value = terraform_data.anchor.output
}
