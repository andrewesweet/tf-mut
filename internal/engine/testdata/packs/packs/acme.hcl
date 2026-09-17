# A user-defined pack in the shipped format: entry blocks only, literal values
# only. Two entries with different labels may ask for one identical rewrite,
# and both become origins of the row it produces.

entry "flag-off" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "flip"
  from          = true
  to            = false
  source_rule   = "ACME-001"
}

entry "flag-off-again" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "flip"
  from          = true
  to            = false
}

entry "acl-public" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "replace"
  from          = "private"
  to            = "public-read"
}

entry "size-zero" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "replace"
  from          = 3
  to            = 0
}

entry "note-drop" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "replace"
  from          = "keep"
  to            = "dropped"
}

entry "label-empty" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "replace"
  from          = "named"
  to            = ""
}
