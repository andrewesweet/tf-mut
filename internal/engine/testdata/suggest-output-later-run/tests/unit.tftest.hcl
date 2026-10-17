run "first" {
  command = apply

  variables {
    flag = true
  }
}

run "second" {
  command = apply

  variables {
    flag = false
  }
}
