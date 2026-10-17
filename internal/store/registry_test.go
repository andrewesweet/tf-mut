package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/store"
)

// errRefused is the recheck refusal the save must propagate.
var errRefused = errors.New("refused")

// TestTheRegistryStoreRoundTripsAndClaimsNothingWhenUnreadable pins the
// provenance registry's contract: a saved registry reads back entry for
// entry, anything unreadable — absent, corrupt, or a foreign version — is
// the empty registry that claims nothing, and Save runs its recheck at the
// rename boundary.
func TestTheRegistryStoreRoundTripsAndClaimsNothingWhenUnreadable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".tf-mut-generated.json")
	registryStore := store.NewFileRegistry()

	// The absent registry claims nothing, with the shipped version.
	empty := registryStore.Load(path)
	if len(empty.Files) != 0 || empty.Version != store.RegistryFormatVersion {
		t.Fatalf("the absent registry loaded as %+v", empty)
	}

	recorded := store.EmptyRegistry()
	recorded.Files["tests/generated.tftest.hcl"] = store.RegistryEntry{
		Digest: "abc123", InputDigest: "def456", Pins: []string{"pin-1"},
	}

	rechecked := false
	if err := registryStore.Save(path, recorded, func() error {
		rechecked = true

		return nil
	}); err != nil {
		t.Fatalf("saving the registry: %v", err)
	}

	if !rechecked {
		t.Fatal("the registry save never ran its rename-boundary recheck")
	}

	loaded := registryStore.Load(path)
	entry, found := loaded.Files["tests/generated.tftest.hcl"]
	if !found || entry.Digest != "abc123" || entry.InputDigest != "def456" ||
		len(entry.Pins) != 1 || entry.Pins[0] != "pin-1" {
		t.Fatalf("the registry round-tripped as %+v", loaded)
	}

	// A recheck refusal is the save's error, and nothing is installed.
	refused := errRefused
	if err := registryStore.Save(path, recorded, func() error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("a refused recheck saved anyway: %v", err)
	}

	// Corruption claims nothing: the empty registry, never an error.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("truncating the registry: %v", err)
	}

	if loaded := registryStore.Load(path); len(loaded.Files) != 0 {
		t.Fatalf("a corrupt registry loaded as %+v", loaded)
	}

	if err := os.WriteFile(path, []byte(`{"version":"9.9.9","files":{}}`), 0o600); err != nil {
		t.Fatalf("writing a foreign registry: %v", err)
	}

	if loaded := registryStore.Load(path); len(loaded.Files) != 0 {
		t.Fatalf("a foreign-version registry loaded as %+v", loaded)
	}
}
