# The suggestion engine's output-metadata module: one output whose value the
# suite pins but whose sensitivity it never asserts, and one object-valued
# output the suite pins only by one member. The sensitive-flag mutant
# survives with a metadata-only delta, and the key-drop mutant with a value
# delta that also rewrites the output's type descriptor, so selection must
# prefer the value and drop the metadata-only delta as unassertable.

output "greeting" {
  value     = "hello"
  sensitive = true
}

output "config" {
  value = {
    name    = "app"
    enabled = true
  }
}
