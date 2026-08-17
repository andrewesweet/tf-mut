run "planned" {
  command = plan

  assert {
    condition     = output.whole_child.echo == "SUPPLIED"
    error_message = "the whole-object read must carry the child output"
  }
}
