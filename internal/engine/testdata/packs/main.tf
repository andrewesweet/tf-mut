# The M5c.1 pack fixture (offline, terraform_data-based): the user-defined
# pack witness. The builtin provider's schema describes terraform_data.input
# as `dynamic` and optional, which is the evidence the pack contract's site
# rule rests on, so every entry of the two registered packs fires here.
#
#   flag   input = true       BOOL-LITERAL-FLIP owns the row; acme's flip
#                             entries and second's are its origins
#   acl    input = "private"  PACK-REPLACE owns the row (acme, acl-public)
#   size   input = 3          NUM-ZERO owns the row; acme and second both ask
#                             for 0, so two packs appear as origins
#   note   input = "keep"     unasserted: the pack survivor suggest works on
#   label  input = "named"    STR-EMPTY owns the row although PACK-REPLACE
#                             sorts first by name; acme's label-empty entry
#                             asks for the same "" and is its origin

resource "terraform_data" "flag" {
  input = true
}

resource "terraform_data" "acl" {
  input = "private"
}

resource "terraform_data" "size" {
  input = 3
}

resource "terraform_data" "note" {
  input = "keep"
}

resource "terraform_data" "label" {
  input = "named"
}

output "flag" {
  value = terraform_data.flag.input
}

output "acl" {
  value = terraform_data.acl.input
}

output "size" {
  value = terraform_data.size.input
}

output "note" {
  value = terraform_data.note.input
}

output "label" {
  value = terraform_data.label.input
}
