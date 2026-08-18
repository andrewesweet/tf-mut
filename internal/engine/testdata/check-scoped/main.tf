# Issue #70: a check block scopes a data source, and Terraform evaluates it
# during `terraform test`. The scoped data source is an effect and carries a
# provider requirement, so both inventories have to walk the check body.

resource "terraform_data" "anchor" {
  input = "steady"
}

check "health" {
  data "terraform_remote_state" "probe" {
    backend = "local"

    config = {
      path = "${path.module}/probe.tfstate"
    }
  }

  assert {
    condition     = data.terraform_remote_state.probe.backend != ""
    error_message = "the probe must name a backend"
  }
}

check "metadata" {
  data "null_data_source" "meta" {
    inputs = {
      probe = "on"
    }
  }

  assert {
    condition     = data.null_data_source.meta.inputs["probe"] == "on"
    error_message = "the metadata probe must be on"
  }
}

output "anchor" {
  value = terraform_data.anchor.input
}
