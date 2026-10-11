output "greeting" {
  value = "hello"
}

module "child" {
  source = "./child"
}
