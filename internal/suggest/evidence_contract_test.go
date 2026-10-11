package suggest_test

import (
	"testing"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// TestAnInstanceCountsOnlyWhereItsRootRecordVanished keeps the instance
// inference from widening: a delta whose only changes under an instance are
// the value body's cannot tell a vanished instance from a vanished value, so
// no count and no vanished root are inferred; with a change at the instance's
// root record the instance is proven and the count follows.
func TestAnInstanceCountsOnlyWhereItsRootRecordVanished(t *testing.T) {
	t.Parallel()

	valueBodyOnly := []fingerprint.Change{
		changeAt(statePrefix+"example_thing.counted[1]].values.id", `"aaaaaaaa"`),
		changeAt(statePrefix+"example_thing.counted[0]].values.id", `"aaaaaaaa"`),
	}
	evidence := suggest.NewEvidence(valueBodyOnly)
	if len(evidence.InstanceCounts) != 0 || len(evidence.VanishedRoots) != 0 {
		t.Fatalf("value-body-only delta counted %v (roots %v), want none",
			evidence.InstanceCounts, evidence.VanishedRoots)
	}

	withRecord := append(append([]fingerprint.Change{}, valueBodyOnly...),
		changeAt(statePrefix+"example_thing.counted[1]].address", `"example_thing.counted[1]"`),
		changeAt(statePrefix+"example_thing.counted[0]].address", `"example_thing.counted[0]"`),
	)
	evidence = suggest.NewEvidence(withRecord)
	if evidence.InstanceCounts["example_thing.counted"] != 2 {
		t.Fatalf("delta with root records counted %v, want example_thing.counted = 2",
			evidence.InstanceCounts)
	}
	if !evidence.VanishedRoots["example_thing.counted"] {
		t.Fatal("delta with the zeroth instance's record did not prove the vanished root")
	}
}
