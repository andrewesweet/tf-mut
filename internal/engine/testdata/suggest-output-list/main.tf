# The suggestion engine's list-valued output module: one output whose value is
# a list of objects. A mutation of the variable default empties the list, and
# the survivor's delta names an element — `outputs.region_list.value[0].…` —
# which is the payload shape the address adapter must carry past the output
# body to render `output.region_list[0].…`.

variable "regions" {
  type = list(object({ short_name = string }))
  default = [
    { short_name = "eu" },
  ]
}

output "region_list" {
  value = var.regions
}
