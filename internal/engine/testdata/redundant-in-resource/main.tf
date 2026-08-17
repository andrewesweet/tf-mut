# The same-resource union case (M3a.2): the conditional inside the resource is
# redundant, so its mutants are fingerprint-identical — and in plan mode the
# resource's own computed attributes (id, output) are unknown. Those unknowns
# are in the mutation's cone by the same-resource attribute union, so the
# honest verdict stays indeterminate-unknown-values, never Unobservable.

resource "terraform_data" "app" {
  input = true ? "fixed" : "fixed"
}

output "echo" {
  value = terraform_data.app.input
}
