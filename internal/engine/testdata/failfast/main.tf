# Fail-fast fixture: three run blocks observe one watched value, so a mutant
# the first run kills leaves two more the invocation no longer needs. The
# second resource has no observing assertion, so its mutants survive with a
# complete record — the contrast the truncation flag is published for.
resource "terraform_data" "watched" {
  input = "kept"
}

resource "terraform_data" "ignored" {
  input = "unwatched"
}

output "watched" {
  value = terraform_data.watched.input
}

output "unwatched" {
  value = terraform_data.ignored.input
}
