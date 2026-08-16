tf-mut {
  test_dir  = "tests"
  min_score = 10
}

operators {
  tier    = "standard"
  exclude = ["STR-CASE"]
}

exclude {
  paths     = ["generated.tf"]
  resources = ["terraform_data.debug"]
}

reporter "sarif" {
  path = "tf-mut.sarif"
}
