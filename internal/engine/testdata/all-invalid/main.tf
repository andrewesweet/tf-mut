# The all-invalid fixture: every mutant the enabled operators generate is
# rejected by terraform validate, so nothing is scored.
#
# Each argument of the child call is the only site its operator fires on, and
# deleting any one of them leaves a required child variable without a value,
# which validate refuses (verified against Terraform v1.15.8: "Missing
# required argument"). Run with EXT-MODULE-INPUT-DELETE selected alone, the
# population is Invalid throughout and the scored set is empty — the shape
# the admission warning exists for.

module "child" {
  source = "./child"

  first  = "one"
  second = "two"
  third  = "three"
}
