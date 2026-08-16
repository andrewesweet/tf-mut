package engine_test

import (
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The three reproduction cases from review R2-5, which caught the round-one
// repair of the deletion gate leaking.

func TestExistingCountIsEmptiedWhenEveryConsumerTolerates(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "count-tolerant")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	deletions := mutantsWithOperator(result, string(mutation.ResourceDelete))
	if len(deletions) != 1 {
		t.Fatalf("expected one instance-set deletion, got %v", sites(result))
	}

	if !strings.Contains(deletions[0].Diff, "count = 0") {
		t.Fatalf("deletion does not empty the instance set:\n%s", deletions[0].Diff)
	}

	if deletions[0].State != report.Killed {
		t.Fatalf("state = %s, want %s", deletions[0].State, report.Killed)
	}
}

func TestIndexedConsumerNeverGetsAnInstanceSetDeletion(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "count-indexed")

	config := baseConfig(t, module)
	config.Preview = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if deletions := mutantsWithOperator(result, string(mutation.ResourceDelete)); len(deletions) != 0 {
		t.Fatalf("an exact-index consumer must fall back to body blanking, got %v", sites(result))
	}

	if blanks := mutantsWithOperator(result, string(mutation.BodyBlank)); len(blanks) == 0 {
		t.Fatalf("expected a body-blank fallback, got %v", sites(result))
	}
}

func TestForEachIsEmptiedAndNeverGainsACount(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "foreach")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	deletions := mutantsWithOperator(result, string(mutation.ResourceDelete))
	if len(deletions) != 1 {
		t.Fatalf("expected one instance-set deletion, got %v", sites(result))
	}

	diff := deletions[0].Diff
	if !strings.Contains(diff, "for_each") {
		t.Fatalf("deletion does not empty for_each:\n%s", diff)
	}

	if strings.Contains(diff, "+  count") || strings.Contains(diff, "+count") {
		t.Fatalf("a for_each resource must never gain a count:\n%s", diff)
	}

	if deletions[0].State != report.Killed {
		t.Fatalf("state = %s, want %s", deletions[0].State, report.Killed)
	}
}

func TestRequiredArgumentsAreNeverDeleted(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "count-tolerant")

	config := baseConfig(t, module)
	config.Preview = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.Operator != string(mutation.AttrDelete) {
			continue
		}

		// terraform_data exposes exactly two optional arguments; count is a
		// meta-argument and is never a deletion site.
		if strings.HasSuffix(mutant.Site, ".count") {
			t.Fatalf("a meta-argument was deleted: %s", mutant.Site)
		}
	}
}

func TestStaticAndDynamicFailuresAreDiscriminated(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := stateOf(t, result, "module.child.needed"); got != report.Invalid {
		t.Fatalf("deleting a required module input = %s, want %s", got, report.Invalid)
	}

	dynamic := extremeStateFor(t, result, "terraform_data.app")
	if dynamic != report.KilledByError {
		t.Fatalf("the dynamic failure = %s, want %s", dynamic, report.KilledByError)
	}

	if result.Count(report.Invalid) == 0 || result.Count(report.KilledByError) == 0 {
		t.Fatalf("expected both states, got %v", result.Metrics.Counts)
	}
}

func TestInvalidMutantsAreExcludedFromEveryScore(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	counts := result.Metrics.Counts
	expected := counts[report.Killed] + counts[report.KilledByError] +
		counts[report.Survived] + counts[report.NoCoverage] + counts[report.Timeout]

	if result.Metrics.Scored != expected {
		t.Fatalf("scored set = %d, want %d (Invalid must be excluded)", result.Metrics.Scored, expected)
	}

	if counts[report.Invalid] == 0 {
		t.Fatal("the fixture must contain an invalid mutant to make the exclusion meaningful")
	}
}

func TestMetricsAreReproducibleFromTheStateCounts(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "discriminate")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	counts := result.Metrics.Counts
	killed := counts[report.Killed]
	killedByError := counts[report.KilledByError]
	survived := counts[report.Survived]
	noCoverage := counts[report.NoCoverage]
	timeouts := counts[report.Timeout]

	scored := killed + killedByError + survived + noCoverage + timeouts

	assertRatio(t, "mutation score", result.Metrics.MutationScore, killed+killedByError, scored)
	assertRatio(t, "assertion score", result.Metrics.AssertionScore, killed, killed+survived+timeouts)
	assertRatio(t, "reachability", result.Metrics.Reachability,
		killed+killedByError+survived+timeouts, scored)
}

func assertRatio(t *testing.T, name string, got float64, numerator, denominator int) {
	t.Helper()

	want := 0.0
	if denominator > 0 {
		want = float64(numerator) / float64(denominator)
	}

	if got != want {
		t.Fatalf("%s = %v, want %d/%d = %v", name, got, numerator, denominator, want)
	}
}

// extremeStateFor returns the state of the resource-level extreme mutant.
func extremeStateFor(t *testing.T, result report.Report, address string) report.State {
	t.Helper()

	for _, mutant := range result.Mutants {
		if mutant.Resource == address && mutant.Operator != string(mutation.ResourceDelete) {
			return mutant.State
		}
	}

	t.Fatalf("no extreme mutant for %s; sites are %v", address, sites(result))

	return ""
}
