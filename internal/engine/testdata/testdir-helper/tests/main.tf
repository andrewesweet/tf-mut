module "setup" {
  source = "./setup"
}

output "ready" {
  value = lower(module.setup.ready)
}
