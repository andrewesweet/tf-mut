# The discover-only promise at the population level: the child declares `name`
# in a `.tf.json` file, so reading that JSON is the only thing that makes the
# native call's input a declared one. It must not add a `ModuleInputDelete`
# mutant the unread closure does not have.

module "child" {
  source = "./child"
  name   = "steady"
}

output "anchor" {
  value = module.child.anchor
}
