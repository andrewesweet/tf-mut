package engine_test

import (
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3a.3 (#47): conditional-instantiation NoCoverage, evaluated against the
// mutant. Quoting C1: "Pre-classification is per-mutant, never per-block: the
// mutated multiplicity expression must be statically zero under every
// relevant run, and any mutant whose site is in or graph-upstream of the
// multiplicity expression always executes."

// runConditional executes a fixture and returns the report.
func runConditional(t *testing.T, fixture string) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, fixture)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	return result
}

// bodyMutants returns the mutants inside a resource's body, excluding its
// multiplicity site.
func bodyMutants(result report.Report, resource, meta string) []report.Mutant {
	mutants := []report.Mutant{}

	for _, mutant := range result.Mutants {
		if mutant.Resource != resource {
			continue
		}

		if mutant.Site == resource+"."+meta || strings.HasPrefix(mutant.Site, resource+"."+meta+".") {
			continue
		}

		mutants = append(mutants, mutant)
	}

	return mutants
}

// TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage is C1's first
// mandatory case, for count and for_each both: statically zero under every
// relevant run means NoCoverage without a single execution.
func TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage(t *testing.T) {
	t.Parallel()

	result := runConditional(t, "conditional-nocoverage")

	for _, block := range []struct{ resource, meta string }{
		{"terraform_data.gated", "count"},
		{"terraform_data.each_gated", "for_each"},
	} {
		mutants := bodyMutants(result, block.resource, block.meta)
		if len(mutants) == 0 {
			t.Fatalf("no body mutants generated for %s; the fixture lost its point", block.resource)
		}

		for _, mutant := range mutants {
			// The whole-block operators mutate the multiplicity itself, and the
			// site-or-upstream rule sends them to execution; only true body
			// sites are pre-classified.
			if mutant.Site == block.resource {
				continue
			}

			if mutant.State != report.NoCoverage {
				t.Errorf("body mutant %s (%s at %s): state %s, want %s",
					mutant.ID, mutant.Operator, mutant.Site, mutant.State, report.NoCoverage)
			}

			if len(mutant.Runs) != 0 {
				t.Errorf("body mutant %s executed %d runs; NoCoverage may never execute",
					mutant.ID, len(mutant.Runs))
			}
		}
	}
}

// TestAMutantInsideTheConditionExecutes is C1's second mandatory case: a
// COND-SWAP of the condition executes and is killable — the run that would
// catch it can never be suppressed.
func TestAMutantInsideTheConditionExecutes(t *testing.T) {
	t.Parallel()

	result := runConditional(t, "conditional-nocoverage")

	found := false

	for _, mutant := range result.Mutants {
		if !strings.HasPrefix(mutant.Site, "terraform_data.gated.count") {
			continue
		}

		found = true

		if mutant.State == report.NoCoverage {
			t.Errorf("in-condition mutant %s (%s) was pre-classified NoCoverage; "+
				"it must execute", mutant.ID, mutant.Operator)
		}

		if mutant.Operator == "COND-SWAP" && mutant.State != report.Killed {
			t.Errorf("COND-SWAP of the condition is %s, want %s: the length assertion "+
				"catches the instantiated resource", mutant.State, report.Killed)
		}
	}

	if !found {
		t.Fatal("no mutant fired inside the count expression; the fixture lost its point")
	}
}

// TestAMutantUpstreamOfTheConditionExecutes is C1's third mandatory case: a
// mutation of the variable the condition reads executes, whatever the run
// overrides make of it.
func TestAMutantUpstreamOfTheConditionExecutes(t *testing.T) {
	t.Parallel()

	result := runConditional(t, "conditional-nocoverage")

	found := false

	for _, mutant := range result.Mutants {
		if !strings.HasPrefix(mutant.Site, "var.enabled") {
			continue
		}

		found = true

		if mutant.State == report.NoCoverage {
			t.Errorf("upstream mutant %s (%s at %s) was pre-classified NoCoverage; "+
				"it must execute", mutant.ID, mutant.Operator, mutant.Site)
		}
	}

	if !found {
		t.Fatal("no mutant fired at var.enabled; the fixture lost its point")
	}
}

// TestExcludedCategoriesFailClosedToExecution is one fixture per excluded
// category: plan_options.target, a run.* reference, and an unsupported
// expression form each keep every body mutant in execution.
func TestExcludedCategoriesFailClosedToExecution(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{
		"conditional-target", "conditional-runref", "conditional-function",
	} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			result := runConditional(t, fixture)

			mutants := bodyMutants(result, "terraform_data.gated", "count")
			if len(mutants) == 0 {
				t.Fatal("no body mutants generated; the fixture lost its point")
			}

			for _, mutant := range mutants {
				if mutant.State == report.NoCoverage {
					t.Errorf("mutant %s (%s at %s) was pre-classified in a fail-closed "+
						"category; it must execute",
						mutant.ID, mutant.Operator, mutant.Site)
				}
			}
		})
	}
}

// TestDecidableToNonzeroControlsClassifyByExecution: where the evaluator
// decides the multiplicity nonzero, pre-classification abstains, and the
// verdicts are identical to a run with the shortcut disabled.
func TestDecidableToNonzeroControlsClassifyByExecution(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "conditional-nonzero")

	static, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	control := baseConfig(t, module)
	control.DisableStaticShortcuts = true

	executed, err := engine.Run(t.Context(), control)
	if err != nil {
		t.Fatalf("control run: %v", err)
	}

	assertVerdictInvariance(t, executed, static)

	for _, mutant := range static.Mutants {
		if mutant.State == report.NoCoverage {
			t.Errorf("mutant %s (%s) is NoCoverage in a decidably nonzero fixture",
				mutant.ID, mutant.Site)
		}
	}
}
