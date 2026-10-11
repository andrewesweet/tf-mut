module "child" {
  source = "./child"

  first    = "one"
  second   = "two"
  third    = "three"
  optional = "four"
}
