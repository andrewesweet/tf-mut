run "outside" {
  command = plan

  module {
    source = "../outside-sibling"
  }
}
