terraform {
  required_providers {
    terraform = {
      source = "terraform.io/builtin/terraform"
    }
  }
}

# The zeroed module: a resource whose count is two whole instances, and whose
# zero-count mutation keeps the declaration and removes every instance. The
# baseline's payload carries both instances, and the resource's root records
# travel with them — the delta proves how large the baseline collection was
# even though the mutant left it empty rather than absent.
resource "terraform_data" "zeroed" {
  count = 2

  input = {
    region = "steady"
  }
}
# No consumer reads the instance set: the module's own suite cannot kill the
# zeroing mutation, so it survives and the generator owes it an assertion.
