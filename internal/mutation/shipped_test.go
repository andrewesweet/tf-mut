package mutation_test

import (
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// TestEveryShippedPackSatisfiesTheUserPackContract holds the shipped packs to
// the normative contract a user pack is loaded under — the same `ParsePack`
// ran at init, and a violation would have panicked the test binary before any
// test ran — and holds shipped entries to the stricter half a user pack may
// skip: every one states its upstream source rule and licence. A shipped pack
// that fails here is a broken build, never a silent skip.
func TestEveryShippedPackSatisfiesTheUserPackContract(t *testing.T) {
	t.Parallel()

	names := mutation.ReservedPackNames()
	if len(names) == 0 {
		t.Fatal("no shipped pack is embedded; a reserved name without a pack cannot happen")
	}

	for _, name := range names {
		pack, found := mutation.ShippedPack(name)
		if !found {
			t.Errorf("reserved name %q has no shipped pack", name)

			continue
		}

		if !pack.Embedded {
			t.Errorf("pack %q is not marked embedded", name)
		}

		if pack.Digest == "" {
			t.Errorf("pack %q carries no digest", name)
		}

		if len(pack.Entries) == 0 {
			t.Errorf("pack %q ships no entry", name)
		}

		seen := map[string]bool{}
		for _, entry := range pack.Entries {
			if seen[entry.ID] {
				t.Errorf("pack %q: entry %q is declared twice", name, entry.ID)
			}

			seen[entry.ID] = true

			if entry.SourceRule == "" || entry.SourceLicence == "" {
				t.Errorf("pack %q: entry %q does not state its source rule and licence",
					name, entry.ID)
			}

			// The form contract, re-checked on the shipped entries' concrete
			// shapes: the init parse enforces the general constraints, and a
			// violation would have panicked before this assertion ran.
			if entry.Form == mutation.FormFlip &&
				(entry.From.Type() != cty.Bool || entry.To.Type() != cty.Bool ||
					entry.From.True() == entry.To.True()) {
				t.Errorf("pack %q: entry %q declares a flip whose ends are not opposite booleans",
					name, entry.ID)
			}
		}
	}
}

// TestReservedPackNamesAreACopyNotTheShippedList: the accessor hands the
// caller a copy, so a caller-side mutation cannot take a reserved name's
// protection away.
func TestReservedPackNamesAreACopyNotTheShippedList(t *testing.T) {
	t.Parallel()

	reserved := mutation.ReservedPackNames()
	want := slices.Clone(reserved)

	reserved[0] = "mutated"

	if !slices.Equal(mutation.ReservedPackNames(), want) {
		t.Fatal("ReservedPackNames leaked its backing array to the caller")
	}
}
