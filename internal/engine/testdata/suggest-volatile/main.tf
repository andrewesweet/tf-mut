resource "null_resource" "app" {
  triggers = {
    env   = "steady"
    token = uuid()
  }
}

resource "null_resource" "steady" {
  triggers = {
    env   = "constant"
    extra = "stable"
  }
}
