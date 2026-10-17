package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/store"
)

// TestTheBaselineStoreRoundTripsAndRefusesToFakeAnEmptyList pins the
// acceptance list's contract: a saved list reads back entry for entry, a
// missing file is the empty adoption state, and a corrupt or foreign-format
// file is an error — never a fake empty list. The written file is
// owner-only.
func TestTheBaselineStoreRoundTripsAndRefusesToFakeAnEmptyList(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".tf-mut-baseline.json")
	baseline := store.NewBaselineFile()

	// A missing file is adoption: an empty list, not an error.
	entries, err := baseline.Load(path)
	if err != nil {
		t.Fatalf("loading the absent baseline: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("the absent baseline loaded %d entries", len(entries))
	}

	accepted := []store.AcceptanceEntry{
		{ID: "MUT-B", Actionable: true, State: "survived", Site: "a.tf:3", Operator: "ARITH-SWAP"},
		{ID: "MUT-A", Actionable: false, State: "unobservable", Site: "a.tf:7", Operator: "BOOL-LIT"},
	}

	if err := baseline.Save(path, accepted); err != nil {
		t.Fatalf("saving the baseline: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating the baseline: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the baseline is mode %o, want 0600", info.Mode().Perm())
	}

	entries, err = baseline.Load(path)
	if err != nil {
		t.Fatalf("loading the baseline: %v", err)
	}

	if len(entries) != 2 || entries[0].ID != "MUT-B" || entries[1].ID != "MUT-A" ||
		!entries[0].Actionable || entries[1].Actionable || entries[1].Operator != "BOOL-LIT" {
		t.Fatalf("the baseline round-tripped as %+v", entries)
	}

	// Corruption is an error: the gate must never read a fake empty list.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("truncating the baseline: %v", err)
	}

	if _, err := baseline.Load(path); !errors.Is(err, store.ErrBaselineFile) {
		t.Fatalf("a corrupt baseline loaded as %v", err)
	}
}
