terraform {
  cloud {
    hostname     = "127.0.0.1"
    organization = "tf-mut"
    workspaces {
      name = "schema-failure"
    }
  }
}

resource "terraform_data" "r" {
  input = "kept"
}
