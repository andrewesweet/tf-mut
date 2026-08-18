# The reader whose presence decides local.orphan's verdict. With it, mutants of
# local.orphan produce an observable delta and survive; without it, their
# forward cone is empty and they are statically Unobservable. Removing this
# resource is edit E3 of the pinned sequence: a verdict-changing dependency
# that lives in a different file from the mutant.

resource "terraform_data" "reader" {
  input = local.orphan
}

output "reader" {
  value = terraform_data.reader.input
}
