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
		{"state output", "outputs.identity.value"},
		{"plan output", "output_changes.identity.after"},
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
