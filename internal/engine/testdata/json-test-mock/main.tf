# The floor's mock-inventory case: the provider is real, and the only thing that
# would make it mocked is the `mock_provider` block in `tests/unit.tftest.json`.
# While that file is unread the mock inventory is unknowable, so the
# real-infrastructure gate cannot be satisfied by it.

terraform {
  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "3.2.4"
    }
  }
}

resource "null_resource" "app" {
  triggers = {
    env = "dev"
  }
}

output "env" {
  value = null_resource.app.triggers.env
}
