# The batch-attribution fixture: one resource the suite asserts and two it
# does not, so one target test file carries several distinct valid
# suggestions for verification to batch together — and one seeded defect to
# make the batch fail.

resource "terraform_data" "watched" {
  input = "watched"
}

resource "terraform_data" "ignored_a" {
  input = "nobody-checks-a"
}

resource "terraform_data" "ignored_b" {
  input = "nobody-checks-b"
}

output "watched" {
  value = terraform_data.watched.input
}

output "ignored_a" {
  value = terraform_data.ignored_a.input
}

output "ignored_b" {
  value = terraform_data.ignored_b.input
}
