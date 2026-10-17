# The non-root half of the F10 classification: every run supplies the root
# variable, and the root call forwards it, so the child's own default is
# genuinely never read — but the module call's input is outside the test-file
# inventory, so a VAR-DEFAULT mutant of the child variable executes as it
# always has. The root variable itself is statically Unobservable.

variable "instance" {
  type    = string
  default = "root-default"
}

module "child" {
  source   = "./child"
  instance = var.instance
}

output "echo" {
  value = module.child.echo
}
