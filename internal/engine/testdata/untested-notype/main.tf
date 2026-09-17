# A required input whose declared type nests past the synthesiser's nesting
# limit: the typed rung produces no candidate at all, so the input stays a
# judgement point carrying the no-candidate gap rather than one carrying a
# refused candidate. This is the opportunity census's no-typed-candidate
# class (M5-0.5a).

variable "deep" {
  type = list(list(list(list(list(list(list(string)))))))
}

resource "terraform_data" "anchor" {
  input = "steady"
}

output "anchor" {
  value = terraform_data.anchor.output
}
