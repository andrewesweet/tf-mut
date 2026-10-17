# The modulecall-registry fixture: an ordinary module call whose source is a
# registry address, not a local path. The checkout boundary never applies to
# it: discovery records the call as remote and never reads a directory for it.

resource "terraform_data" "subject" {
  input = "old"
}

module "registry" {
  source = "hashicorp/consul/aws"
}
