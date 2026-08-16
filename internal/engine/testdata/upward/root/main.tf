module "shared" {
  source = "../shared"
  prefix = "root"
}

output "name" {
  value = module.shared.name
}
