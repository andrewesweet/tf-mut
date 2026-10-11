# The sampled-unanswered fixture: the unanswerable-resources warning is
# decided over the generated population, not over whatever the sample kept.
#
# terraform_data.bare declares nothing, so no enabled operator produces any
# mutant for it — the warning must name it. terraform_data.left and
# terraform_data.right each carry literal arguments an operator reaches, so a
# hard sample that keeps a single mutant must leave at least one of them with
# mutants in the full population and none in the executed one. A resource the
# sample merely dropped is not a resource the tool says nothing about.

resource "terraform_data" "bare" {
}

resource "terraform_data" "left" {
  input = "left-value"
}

resource "terraform_data" "right" {
  input = "right-value"
}

output "left" {
  value = terraform_data.left.input
}

output "right" {
  value = terraform_data.right.input
}
