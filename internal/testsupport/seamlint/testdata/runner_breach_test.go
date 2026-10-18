package engine_test

import (
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// constructRunner deliberately builds the Terraform runner two ways below
// the seam. It is the seam lint's breaching fixture: the engine builds the
// one runner a run gets, from the request, against the real binary, so a
// test constructing its own runner is standing up a driver the rule refuses.
// The variable declaration remains allowed by the construction check.
func constructRunner() {
	literal := tfexec.Runner{Binary: "terraform"}
	var declared tfexec.Runner
	heap := new(tfexec.Runner)

	literal.Env = nil
	declared.Binary = "terraform"
	heap.Binary = "terraform"
}
