package tfexec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// backendDemandFixture is the one schema failure a fixture can stage offline:
// a root declaring a backend that init never initialised. tf-mut initialises
// with -backend=false, so a backend-declaring root makes the next command —
// providers schema — demand backend initialisation. Its text is what this
// test pins: init, validate, fmt and test all run with -no-color, and the
// schema call must colour its failure the same way, or every reporter
// reprints the demand wrapped in ANSI escapes.
const backendDemandFixture = `terraform {
  backend "azurerm" {}
}

resource "terraform_data" "r" {
  input = "kept"
}
`

func TestProvidersSchemaFailureCarriesNoColourCodes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(backendDemandFixture), 0o600); err != nil {
		t.Fatalf("writing main.tf: %v", err)
	}

	runner := tfexec.Runner{
		Binary: "terraform",
		Env:    []string{"CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1"},
	}

	if err := runner.Init(t.Context(), dir, tfexec.InitOptions{}); err != nil {
		t.Fatalf("init: %v", err)
	}

	_, err := runner.ProvidersSchema(t.Context(), dir)
	if err == nil {
		t.Fatal("providers schema succeeded; want the backend-initialisation demand")
	}

	if !strings.Contains(err.Error(), "Backend initialization required") {
		t.Fatalf("error = %v, want the backend-initialisation demand", err)
	}

	if strings.Contains(err.Error(), "\x1b[") {
		t.Fatalf("error text carries ANSI colour codes: %q", err.Error())
	}
}
