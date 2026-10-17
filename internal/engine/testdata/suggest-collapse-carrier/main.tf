# The collapse-carrier fixture (D13/D14): the utl-regions `locals.tf:12` shape — a
# list of objects with a `short_name` the suite never asserts. Value mutants of
# the name and absence mutants that drop it survive with deltas at the same
# output element, so generation folds them into one suggestion: the carrier
# plus its also_kills members.
locals {
  regions = [
    { short_name = "wu" },
  ]
}

output "regions" {
  value = local.regions
}
