terraform {
  required_providers {
    terraform = {
      source = "terraform.io/builtin/terraform"
    }
  }
}

# The count module: a resource whose count is two whole instances. A count
# mutation removes an instance, and every payload path under the vanished
# index proves, by its baseline side alone, that the baseline had a collection
# one instance larger than the mutant left.
resource "terraform_data" "counted" {
  count = 2

  input = {
    region = "steady"
  }
}
