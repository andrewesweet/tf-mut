# The census's second preview-refusal fixture: a module declaring an `import`
# block, a construct this version does not model.
#
# Discovery refuses the module before any population question arises, so the
# census records the population as unknown with preview's own refusal text.

resource "terraform_data" "subject" {
  input = "old"
}

import {
  to = terraform_data.subject
  id = "offline-id"
}
