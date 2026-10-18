package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
)

// TestCleanFixture drives the seam exactly as the rule prescribes: a request
// in, a report out, and no settings value, no runner and no engine-driven
// import anywhere. The seam lint must leave it alone.
func TestCleanFixture(t *testing.T) {
	t.Parallel()

	reported, err := engine.Run(context.Background(), engine.RunRequest{
		Common: engine.Common{
			ModuleDir:       "fixture",
			TerraformBinary: "terraform",
			TimeoutFloor:    time.Second,
		},
	})
	if err != nil {
		t.Fatalf("running the seam: %v", err)
	}

	if reported.TerraformVersion == "" {
		t.Fatal("the report carries no Terraform version")
	}
}
