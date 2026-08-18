package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/engine"
)

// Issue #70 and the M4.5 spec review's C4: `check` and `removed` bodies are
// collected into the effect and provider inventories in both syntaxes, and
// `moved` and `import` remain explicitly refused in both readers. Every case
// below is a fail-open edge before the collector exists: the construct's
// content reaches Terraform and neither gate.

const (
	checkScopedFixture   = "check-scoped"
	removedScopedFixture = "removed-scoped"
	contractFixture      = "contract"
)

func TestACheckScopedDataSourceReachesTheProviderInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, checkScopedFixture)

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal for the check-scoped provider", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("the refusal does not name the check-scoped provider: %v", err)
	}
}

func TestACheckScopedDataSourceReachesTheEffectInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, checkScopedFixture)

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal for the check-scoped probe", err)
	}

	if !strings.Contains(err.Error(), "terraform_remote_state") {
		t.Fatalf("the refusal does not name the check-scoped effect: %v", err)
	}

	if !strings.Contains(err.Error(), "main.tf:") {
		t.Fatalf("the refusal does not carry a source range: %v", err)
	}
}

func TestARemovedBlockReachesTheProviderInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, removedScopedFixture)

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal for the removed block's provider", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("the refusal does not name the provider the destroy would reach: %v", err)
	}
}

func TestARemovedBlockProvisionerReachesTheEffectInventoryAndNeverExecutes(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, removedScopedFixture)
	marker := filepath.Join(t.TempDir(), "destroy-provisioner-ran")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal for the destroy-time provisioner", err)
	}

	if !strings.Contains(err.Error(), "provisioner") {
		t.Fatalf("the refusal does not name the offending construct: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the destroy-time provisioner executed despite the refusal")
	}
}

// TestACheckScopedDataSourceInJSONReachesTheEffectInventory is issue #70's JSON
// half. Before the collector the file was left unread and the floor stood in
// for a reading; now the check body is read and the effect gate refuses on the
// content itself.
func TestACheckScopedDataSourceInJSONReachesTheEffectInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "checks.tf.json"),
		`{"check":{"health":{"data":{"terraform_remote_state":{"probe":{"backend":"local"}}},`+
			`"assert":[{"condition":"${data.terraform_remote_state.probe.backend != \"\"}",`+
			`"error_message":"probe"}]}}}`+"\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal: a check-scoped data source "+
			"is an effect the inventory now carries", err)
	}

	if !strings.Contains(err.Error(), "checks.tf.json") {
		t.Fatalf("the refusal does not name the file the effect lives in: %v", err)
	}
}

func TestACheckScopedDataSourceInJSONReachesTheProviderInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "checks.tf.json"),
		`{"check":{"metadata":{"data":{"null_data_source":{"meta":{"inputs":{"probe":"on"}}}},`+
			`"assert":[{"condition":"${data.null_data_source.meta.inputs[\"probe\"] == \"on\"}",`+
			`"error_message":"meta"}]}}}`+"\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal for the JSON check-scoped provider", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("the refusal does not name the check-scoped provider: %v", err)
	}
}

func TestARemovedBlockInJSONReachesTheEffectInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "removals.tf.json"),
		`{"removed":[{"from":"${terraform_data.gone}","lifecycle":{"destroy":true},`+
			`"provisioner":{"local-exec":{"when":"destroy","command":"touch $TF_MUT_MARKER"}}}]}`+"\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal for the JSON destroy-time provisioner", err)
	}

	if !strings.Contains(err.Error(), "removals.tf.json") {
		t.Fatalf("the refusal does not name the file the effect lives in: %v", err)
	}
}

func TestARemovedBlockInJSONReachesTheProviderInventory(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "removals.tf.json"),
		`{"removed":[{"from":"${null_resource.gone}","lifecycle":{"destroy":true}}]}`+"\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal for the JSON removed block's provider", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("the refusal does not name the provider the destroy would reach: %v", err)
	}
}

// TestAMovedBlockIsRefusedInHCL and its import twin hold the other half of the
// C4 disposition: the two constructs that stay uncollected are refused by name
// rather than skipped in silence, which is what kept `check` invisible for a
// whole milestone.
func TestAMovedBlockIsRefusedInHCL(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "moves.tf"),
		"moved {\n  from = terraform_data.old\n  to   = terraform_data.anchor\n}\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrUnmodelledConstruct) {
		t.Fatalf("error = %v, want a refusal naming the construct this version does not model", err)
	}

	if !strings.Contains(err.Error(), "moved") || !strings.Contains(err.Error(), "moves.tf") {
		t.Fatalf("the refusal names neither the construct nor its file: %v", err)
	}
}

func TestAnImportBlockIsRefusedInHCL(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "imports.tf"),
		"import {\n  to = terraform_data.anchor\n  id = \"anchor\"\n}\n")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, discovery.ErrUnmodelledConstruct) {
		t.Fatalf("error = %v, want a refusal naming the construct this version does not model", err)
	}

	if !strings.Contains(err.Error(), "import") {
		t.Fatalf("the refusal does not name the construct: %v", err)
	}
}

// TestAMovedBlockInJSONIsRefusedByName is the other half of the C4 disposition
// in the syntax that has a floor.
//
// Leaving the construct out of the schema would only leave the file *unread*,
// and a floor is one opt-in away from being lifted: grant
// --allow-real-infrastructure and --allow-unsandboxed-effects and the run
// proceeds with the construct represented nowhere. A refusal is not
// overridable, which is what a construct this version cannot model needs — so
// the refusal is asserted with both opt-ins granted.
func TestAMovedBlockInJSONIsRefusedByName(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, jsonProviderFixture)
	writeFile(t, filepath.Join(module, "moves.tf.json"),
		`{"moved":[{"from":"${terraform_data.old}","to":"${terraform_data.anchor}"}]}`+"\n")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true
	config.AllowUnsandboxedEffects = true

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, discovery.ErrUnmodelledConstruct) {
		t.Fatalf("error = %v, want a refusal no opt-in can override", err)
	}

	if !strings.Contains(err.Error(), "moves.tf.json") {
		t.Fatalf("the refusal does not name the file: %v", err)
	}
}

// TestAnImportBlockInJSONIsRefusedByName is its twin.
func TestAnImportBlockInJSONIsRefusedByName(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	writeFile(t, filepath.Join(module, "imports.tf.json"),
		`{"import":[{"to":"${terraform_data.anchor}","id":"anchor"}]}`+"\n")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true
	config.AllowUnsandboxedEffects = true

	if _, err := engine.Run(t.Context(), config); !errors.Is(err, discovery.ErrUnmodelledConstruct) {
		t.Fatalf("error = %v, want a refusal no opt-in can override", err)
	}
}
