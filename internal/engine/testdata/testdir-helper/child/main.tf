module "leaf" {
  source = "../leaf"
}

output "ready" {
  value = upper(module.leaf.ready)
}
