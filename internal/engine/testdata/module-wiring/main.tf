# The #44 re-review's two reproduction shapes. The remote call's wiring is
# unmodellable — a changed input can alter everything the remote module does —
# so its nodes make any cone unbounded. The whole-object read of the local
# child must draw a real edge rather than being silently dropped.
# Discovery-only: the remote source is never fetched offline.

variable "remote_prefix" {
  type    = string
  default = "edge"
}

module "remote" {
  source = "git::https://example.invalid/unreachable//mod"
  prefix = var.remote_prefix
}

module "child" {
  source = "./child"
  needed = "supplied"
}

output "whole_child" {
  value = module.child
}
