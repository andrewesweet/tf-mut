variables {
  probe = "constant"
}

run "applied" {
  command = apply

  assert {
    condition     = output.app == "app-dev"
    error_message = "the app input must survive"
  }

  # Reads the output weakly: it senses far less than the assertion above, and
  # every mutant it catches that one catches too.
  assert {
    condition     = output.app != null
    error_message = "this assertion senses less than the one above it"
  }

  # Reads the test's own input and nothing the module produces. No mutation of
  # the module can falsify it, so no mutant's death can depend on it. This is
  # failure mode D1's shape: an assertion whose kill set is empty.
  assert {
    condition     = var.probe == "constant"
    error_message = "this assertion cannot fail whatever the module does"
  }
}

# A second scenario whose assertion catches exactly what the first one catches:
# its inputs do not discriminate the behaviour, which is the redundancy curate
# has power over.
run "applied_again" {
  command   = apply
  state_key = "applied_again"

  assert {
    condition     = output.app == "app-dev"
    error_message = "the app input must survive here too"
  }
}
