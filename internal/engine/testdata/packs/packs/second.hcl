# A second user-defined pack whose entries request bytes the first also
# produces: both packs must appear as origins on the surviving rows.

entry "flag-clear" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "flip"
  from          = true
  to            = false
}

entry "size-nought" {
  resource_type = "terraform_data"
  attribute     = "input"
  form          = "replace"
  from          = 3
  to            = 0
}
