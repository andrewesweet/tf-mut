package cmd_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/store"
)

// TestImportFixture deliberately imports a package only the engine drives.
// It is the seam lint's breaching fixture: the scanner must flag the import,
// because a test outside the engine and outside the package's own tests has
// no business reaching the engine's persistence implementations. It never
// runs; only the import and the shape it forces matter.
func TestImportFixture(t *testing.T) {
	t.Parallel()

	if store.NewBaselineFile() == nil {
		t.Fatal("the fixture never runs; it only carries the import")
	}
}
