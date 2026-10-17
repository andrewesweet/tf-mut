# The JSON variant of modulecall-outside: the only call to the escaping child
# lives in a `.tf.json` file, so the refusal has to ride on the JSON reader's
# inventory the same way the closure does.

resource "terraform_data" "subject" {
  input = "old"
}

output "subject" {
  value = terraform_data.subject.input
}
