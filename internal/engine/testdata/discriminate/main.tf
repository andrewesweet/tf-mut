module "child" {
  source = "./child"
  needed = "supplied"
}

resource "terraform_data" "app" {
  input = {
    name = "app"
  }
}

resource "terraform_data" "consumer" {
  input = terraform_data.app.input.name
}

output "echo" {
  value = module.child.echo
}
