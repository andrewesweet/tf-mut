run "child_run" {
  command = apply

  module {
    source = "./child"
  }
}
