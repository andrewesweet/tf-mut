package suggest_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// TestAnInstanceCountsOnlyWhereItsRootRecordVanished keeps the instance
// inference from widening: a delta whose only changes under an instance are
// the value body's cannot tell a vanished instance from a vanished value, so
// no count is inferred; with a change at the instance's root record the
// instance is proven and the count follows.
func TestAnInstanceCountsOnlyWhereItsRootRecordVanished(t *testing.T) {
	t.Parallel()

	for _, representation := range []struct {
		name, prefix, body string
	}{
		{"state", statePrefix, ".values.id"},
		{"plan", "resource_changes[", ".change.after.id"},
	} {
		for _, record := range []string{"", ".address", ".index"} {
			t.Run(representation.name+"/"+record, func(t *testing.T) {
				t.Parallel()

				valueBodyOnly := []fingerprint.Change{
					changeAt(representation.prefix+"example_thing.counted[1]]"+representation.body, steadyBaseline),
					changeAt(representation.prefix+"example_thing.counted[0]]"+representation.body, steadyBaseline),
				}
				changes := append([]fingerprint.Change{}, valueBodyOnly...)
				want := `try(example_thing.counted[1].id, null) == "steady"`
				if record != "" {
					want = baselineCountAssertion

					first, second := "0", "1"
					if record == ".address" {
						first, second = `"example_thing.counted[0]"`, `"example_thing.counted[1]"`
					}

					changes = append(changes,
						changeAt(representation.prefix+"example_thing.counted[1]]"+record, second),
						changeAt(representation.prefix+"example_thing.counted[0]]"+record, first),
					)
				}

				expression, err := suggest.Express(untyped(),
					suggest.NewEvidence(changes), valueBodyOnly[0])
				assertAdapter(t, expression, err, want, nil)
			})
		}
	}
}

func TestANulledValueRequiresAnUnambiguousBaselineMember(t *testing.T) {
	t.Parallel()

	for _, representation := range []struct {
		name, prefix string
	}{
		{"state resource", statePrefix + "example_thing.app].values.triggers"},
		{"plan resource", "resource_changes[example_thing.app].change.after.triggers"},
		{"state output", identityStatePath},
		{"plan output", identityChangePath},
	} {
		for _, suffix := range []string{".b", firstElementIndex} {
			t.Run(representation.name+suffix, func(t *testing.T) {
				t.Parallel()

				nulled := nulledAt(representation.prefix + ".a")
				changes := []fingerprint.Change{
					nulled,
					changeAt(nulled.Path+suffix, steadyBaseline),
				}
				expression, err := suggest.Express(
					schemaTyping("triggers", `["map","string"]`), suggest.NewEvidence(changes), nulled,
				)
				assertAdapter(t, expression, err, "", suggest.ErrUnrenderable)
			})
		}
	}
}

func TestOutputContainersUseStructuralNonNullEvidence(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{regionsStateValue, regionsPlanAfter} {
		for _, suffix := range []string{".member", firstElementIndex} {
			for _, baseline := range []string{steadyBaseline, nulledValue} {
				t.Run(prefix+suffix+baseline, func(t *testing.T) {
					t.Parallel()

					nulled := nulledAt(prefix)
					evidence := suggest.NewEvidence([]fingerprint.Change{nulled, changeAt(prefix+suffix, baseline)})
					expression, err := suggest.Express(untyped(), evidence, nulled)
					assertAdapter(t, expression, err, "output.regions != null", nil)
				})
			}
		}
	}
	for _, suffix := range []string{".member", firstElementIndex} {
		t.Run("unknown"+suffix, func(t *testing.T) {
			t.Parallel()

			nulled := nulledAt(regionsPlanAfter)
			evidence := suggest.NewEvidence([]fingerprint.Change{
				nulled, changeAt("output_changes.regions.after_unknown"+suffix, "true"),
			})
			expression, err := suggest.Express(untyped(), evidence, nulled)
			assertAdapter(t, expression, err, "output.regions != null", nil)
			if evidence.ProvenNonNull[nulled.Path+suffix] {
				t.Fatal("an unknown leaf was inferred non-null from its own marker")
			}
		})
	}
}
