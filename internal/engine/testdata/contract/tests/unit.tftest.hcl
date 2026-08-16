# The plan-mode run comes first on purpose. A plan payload almost always carries
# unknown values, so this is where an ordering that put the unknown rule above
# `StructurallyUnassertable` would hide every contract finding.
run "planned" {
  command = plan

  assert {
    condition     = terraform_data.first.input == "one"
    error_message = "the first input must be planned"
  }
}

run "applied" {
  command = apply

  assert {
    condition     = output.both == "one-two"
    error_message = "both inputs must be present"
  }
}
