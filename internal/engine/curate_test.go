package engine_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M4.5c: the until-dry loop over the staged suite, and curate's
// authoritative-population posture.

// TestUntilDryConvergesWithoutWritingAByte is the M2 disposition's mandatory
// end-to-end case: the generated suite exists as an overlay that discovery,
// mutation execution, suggestion targeting and verification all consume, and
// the source tree is byte-identical when the loop stops.
func TestUntilDryConvergesWithoutWritingAByte(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedBranchesFixture)
	before := treeDigest(t, module)

	config := characteriseConfig(t, module)
	config.UntilDry = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --until-dry: %v", err)
	}

	if !maps.Equal(treeDigest(t, module), before) {
		t.Fatal("the until-dry loop changed the source tree")
	}

	convergence := result.Characterisation.Convergence
	if convergence == nil {
		t.Fatal("the loop reported no convergence evidence")
	}

	if convergence.Rounds == 0 || len(convergence.NewPinsPerRound) != convergence.Rounds {
		t.Fatalf("the convergence evidence is incomplete: %+v", convergence)
	}

	if convergence.StopReason != "dry" && convergence.StopReason != "bounded" {
		t.Fatalf("stop reason = %s, want dry or bounded", convergence.StopReason)
	}

	if !result.Characterisation.Staged {
		t.Fatal("the report does not say the suite was staged")
	}
}

// TestUntilDryRespectsTheGranularityLadder is the M10 constraint: an
// unconstrained loop drives monotonically towards pinning every configured
// attribute, which is the brittle level reached without the user choosing it.
func TestUntilDryRespectsTheGranularityLadder(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedBranchesFixture)

	config := characteriseConfig(t, module)
	config.UntilDry = true
	config.PinRung = rungOutputs

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --until-dry: %v", err)
	}

	for _, pin := range result.Characterisation.Pins {
		if pin.Status != report.Pinned {
			continue
		}

		if pin.Rung != rungOutputs {
			t.Fatalf("the loop pinned %s at the %s rung, above the chosen outputs rung: %s",
				pin.Address, pin.Rung, pin.Expression)
		}
	}
}

// TestCurateRefusesAPartialPopulationAtConfigurationTime is C5: a redundancy
// finding drawn from a scoped or sampled population is a false finding, and
// the refusal costs nothing because it happens before any work is done.
func TestCurateRefusesAPartialPopulationAtConfigurationTime(t *testing.T) {
	t.Parallel()

	partial := map[string]func(engine.Config) engine.Config{
		"--since": func(config engine.Config) engine.Config {
			config.Since = "HEAD"

			return config
		},
		"--sample": func(config engine.Config) engine.Config {
			config.HasSample = true
			config.SamplePercent = 50

			return config
		},
		"an operator selection": func(config engine.Config) engine.Config {
			config.IncludeOperators = []string{"BOOL-FLIP"}

			return config
		},
		"an exclusion": func(config engine.Config) engine.Config {
			config.ExcludePaths = []string{"main.tf"}

			return config
		},
	}

	for name, adjust := range partial {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "suggest-basic")

			config := baseConfig(t, module)
			config.Curate = true

			_, err := engine.Run(t.Context(), adjust(config))
			if !errors.Is(err, engine.ErrCuratePopulation) {
				t.Fatalf("error = %v, want a refusal of the partial population", err)
			}

			if !strings.Contains(err.Error(), "false finding") {
				t.Fatalf("the refusal does not say why: %v", err)
			}
		})
	}
}

// TestCurateReportsAnEmptyKillSetWithItsEvidence is the finding the oracle has
// power over: an assertion no mutant's death depended on senses nothing the
// catalogue models.
func TestCurateReportsAnEmptyKillSetWithItsEvidence(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, curateFixture)

	config := baseConfig(t, module)
	config.Curate = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("curate: %v", err)
	}

	if result.Command != report.CommandCurate {
		t.Fatalf("command = %s, want curate", result.Command)
	}

	findings := result.Characterisation.Findings
	if len(findings) == 0 {
		t.Fatal("curate reported nothing over a suite with a redundant assertion")
	}

	empty := 0

	for _, finding := range findings {
		if !finding.PopulationAuthoritative {
			t.Fatalf("a finding is not flagged authoritative: %+v", finding)
		}

		if len(finding.Provenance) != len(finding.Members) {
			t.Fatalf("a finding's provenance does not cover its members: %+v", finding)
		}

		for _, class := range finding.Provenance {
			if class != report.PreExisting && class != report.GeneratedEdited {
				t.Fatalf("curate drew a conclusion about a %s assertion", class)
			}
		}

		if finding.Kind == report.EmptyKillSet {
			empty++
		}
	}

	if empty == 0 {
		t.Fatal("the assertion that senses nothing was not reported")
	}
}

// TestCurateWritesNothing keeps the report-only contract: `--apply` is
// deferred until it earns its own recorded write exception.
func TestCurateWritesNothing(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, curateFixture)
	before := treeDigest(t, module)

	config := baseConfig(t, module)
	config.Curate = true
	config.NoCache = true

	if _, err := engine.Run(t.Context(), config); err != nil {
		t.Fatalf("curate: %v", err)
	}

	if !maps.Equal(treeDigest(t, module), before) {
		t.Fatal("curate changed the source tree")
	}
}

// TestOneMutantFailingTwoAssertionsAttributesBoth is the M7 platform-fact
// measurement, taken in the slice that assumes it: Terraform v1.15.8 reports
// every failed assertion of a run with its own source range, in declaration
// order, whichever failed first — so kill-set participation is read rather
// than reconstructed, and no indeterminate status is needed.
func TestOneMutantFailingTwoAssertionsAttributesBoth(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{
		"declared order": killsetFixture,
		"reversed order": killsetReversedFixture,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, fixture)

			config := baseConfig(t, module)
			config.NoCache = true

			result, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			if countMultiAssertionKills(result) == 0 {
				t.Fatal("no mutant failed two assertions with both attributed to distinct ranges")
			}
		})
	}
}

// countMultiAssertionKills counts the mutants whose death was attributed to
// more than one assertion.
func countMultiAssertionKills(result report.Report) int {
	attributed := 0

	for _, mutant := range result.Mutants {
		if mutant.State != report.Killed {
			continue
		}

		failures := map[int]bool{}

		for _, diagnostic := range mutant.Diagnostics {
			if diagnostic.Summary == "Test assertion failed" && diagnostic.Range != nil {
				failures[diagnostic.Range.Start.Line] = true
			}
		}

		if len(failures) > 1 {
			attributed++
		}
	}

	return attributed
}

const (
	curateFixture          = "curate"
	killsetFixture         = "killset"
	killsetReversedFixture = "killset-reversed"
)

// TestUnassertableConstructsBecomeNonExecutableScaffolds is the M4 spec
// review's C4 relocation: skeleton generation for constructs the oracle cannot
// assert on lands here, as a separate artefact class that is never verified,
// never executable, and never touched by `suggest --apply`.
func TestUnassertableConstructsBecomeNonExecutableScaffolds(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, contractFixture)
	removeTests(t, module)

	config := characteriseConfig(t, module)
	config.UntilDry = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --until-dry: %v", err)
	}

	block := result.Characterisation
	if len(block.Scaffolds) == 0 {
		t.Fatal("a module full of unassertable constructs produced no scaffolds")
	}

	for _, scaffold := range block.Scaffolds {
		if scaffold.Status != report.Scaffolded {
			t.Fatalf("scaffold %s is %s before anybody verified it", scaffold.ID, scaffold.Status)
		}

		if scaffold.Kind != "expect_failures" || scaffold.Address == "" {
			t.Fatalf("scaffold %s names no construct: %+v", scaffold.ID, scaffold)
		}
	}

	artefacts := 0

	for _, file := range block.Files {
		if strings.HasSuffix(file.Path, ".tfmut-todo.hcl") {
			artefacts++

			if file.Executable {
				t.Fatalf("%s is in the executable class", file.Path)
			}

			if !strings.Contains(file.Content, "expect_failures") {
				t.Fatalf("%s proposes no check:\n%s", file.Path, file.Content)
			}
		}
	}

	if artefacts != 1 {
		t.Fatalf("the scaffolds travel in %d artefacts, want one", artefacts)
	}
}

// removeTests strips a fixture's suite, turning it into the untested module
// characterisation exists for.
func removeTests(t *testing.T, module string) {
	t.Helper()

	if err := os.RemoveAll(filepath.Join(module, "tests")); err != nil {
		t.Fatalf("removing the test directory: %v", err)
	}
}

// TestAnAnsweredScaffoldIsVerifiedBeforeItIsPromoted is the C2 workflow's
// scaffold half: promotion is earned by execution, never granted. The answer
// supplies the inputs that make the construct fail; a run block asserting a
// failure that does not happen is a failing run block, which is what makes the
// check worth running.
func TestAnAnsweredScaffoldIsVerifiedBeforeItIsPromoted(t *testing.T) {
	t.Parallel()

	for name, answer := range map[string]string{
		"a failing input promotes":       "{ size = 0 }",
		"an input that does not promote": "{ size = 2 }",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, contractFixture)
			removeTests(t, module)

			config := characteriseConfig(t, module)
			config.UntilDry = true

			opened, err := engine.Run(t.Context(), config)
			if err != nil {
				t.Fatalf("characterise --until-dry: %v", err)
			}

			identifier := scaffoldFor(t, opened.Characterisation, "var.size.validation")

			answered := characteriseConfig(t, module)
			answered.UntilDry = true
			answered.Answers = []string{identifier + "=" + answer}

			result, err := engine.Run(t.Context(), answered)
			if err != nil {
				t.Fatalf("characterise --until-dry --answer: %v", err)
			}

			promoted := answer == "{ size = 0 }"
			assertScaffoldPromotion(t, result, identifier, promoted)
		})
	}
}

// assertScaffoldPromotion checks the scaffold's status and whether its check
// became test content.
func assertScaffoldPromotion(t *testing.T, result report.Report, identifier string, promoted bool) {
	t.Helper()

	block := result.Characterisation
	wanted := report.Scaffolded

	if promoted {
		wanted = report.ScaffoldPromoted
	}

	for _, scaffold := range block.Scaffolds {
		if scaffold.ID != identifier {
			continue
		}

		if scaffold.Status != wanted {
			t.Fatalf("scaffold %s is %s, want %s", identifier, scaffold.Status, wanted)
		}
	}

	executable := false

	for _, file := range block.Files {
		if strings.Contains(file.Path, identifier) && file.Executable {
			executable = true

			if !strings.Contains(file.Content, "expect_failures = [var.size]") {
				t.Fatalf("the promoted check names no checkable object:\n%s", file.Content)
			}
		}
	}

	if executable != promoted {
		t.Fatalf("executable check present = %v, want %v", executable, promoted)
	}

	if !promoted && len(result.Warnings) == 0 {
		t.Fatal("a refused promotion says nothing about why")
	}
}

// scaffoldFor finds the scaffold recorded for one construct address.
func scaffoldFor(t *testing.T, block *report.Characterisation, address string) string {
	t.Helper()

	for _, scaffold := range block.Scaffolds {
		if scaffold.Address == address {
			return scaffold.ID
		}
	}

	t.Fatalf("no scaffold was recorded for %s", address)

	return ""
}
