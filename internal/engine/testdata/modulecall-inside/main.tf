# The modulecall-inside fixture: an ordinary module call whose local source
# stays inside the checkout, so the closure follows it and the refusal never
# fires. The child's own resource proves the call was followed: its mutants
# appear under the child's module path.

variable "prefix" {
  type    = string
  default = "edge"
}

module "child" {
  source = "./child"
  needed = var.prefix
}

output "echo" {
  value = module.child.echo
}
