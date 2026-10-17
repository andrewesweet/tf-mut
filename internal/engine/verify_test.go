package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// The suggestion-soundness gate (M4b.1). Both verification legs, both seeded
// defects, and the attribution the isolated leg is what proves.

func TestVerifiedRequiresBothLegsAndCarriesTheirEvidence(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestBasicFixture)))

	verified := withStatus(result, report.SuggestionVerified)
	if len(verified) == 0 {
		t.Fatalf("nothing verified; statuses were %s", statusSummary(result.Suggestions))
	}

	for _, suggestion := range verified {
		if suggestion.Verification == nil {
			t.Fatalf("verified suggestion %s carries no evidence", suggestion.ID)
		}

		if !suggestion.Verification.Baseline.Passed {
			t.Fatalf("suggestion %s is verified with a failed baseline leg", suggestion.ID)
		}

		if !suggestion.Verification.Mutant.Passed {
			t.Fatalf("suggestion %s is verified with a failed mutant leg", suggestion.ID)
		}

		if len(suggestion.Verification.Baseline.Runs) == 0 ||
			len(suggestion.Verification.Mutant.Runs) == 0 {
			t.Fatalf("suggestion %s carries no run references", suggestion.ID)
		}

		if suggestion.VerifiedDigest == "" {
			t.Fatalf("verified suggestion %s carries no source digest", suggestion.ID)
		}
	}
}

// TestASeededWrongValueIsRefutedThroughTheBaselineLeg is the first half of the
// gate: an assertion that breaks the suite must never reach `verified`.
//
//nolint:paralleltest // owns package-global suggestion hook for its lifetime.
func TestASeededWrongValueIsRefutedThroughTheBaselineLeg(t *testing.T) {
	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	engine.SetSuggestionDefectSeed(t, config.ModuleDir, suggest.DefectWrongValue)

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) == 0 {
		t.Fatalf("the seeded wrong value was not refuted: %s", statusSummary(result.Suggestions))
	}

	for _, suggestion := range refuted {
		if suggestion.Verification.Baseline.Passed {
			t.Fatalf("suggestion %s was refuted but its baseline leg passed", suggestion.ID)
		}

		if suggestion.StatusReason == "" {
			t.Fatalf("refuted suggestion %s carries no reason", suggestion.ID)
		}
	}
}

// TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg is the second half,
// and the attribution proof with it: the vacuous assertion is applied beside
// real ones that do kill its mutant, so only the isolated check can see that it
// does not kill anything itself.
//
//nolint:paralleltest // owns package-global suggestion hook for its lifetime.
func TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg(t *testing.T) {
	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	engine.SetSuggestionDefectSeed(t, config.ModuleDir, suggest.DefectVacuous)

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) != 1 {
		for _, suggestion := range result.Suggestions {
			t.Logf("%s %s expr=%q reason=%q verification=%+v", suggestion.ID, suggestion.Status,
				suggestion.Expression, suggestion.StatusReason, suggestion.Verification)
		}

		t.Fatalf("want exactly the seeded vacuous suggestion refuted, got %s",
			statusSummary(result.Suggestions))
	}

	for _, suggestion := range result.Suggestions {
		t.Logf("%s %s expr=%q reason=%q", suggestion.ID, suggestion.Status,
			suggestion.Expression, suggestion.StatusReason)
	}

	seeded := refuted[0]

	left, right, isEquality := strings.Cut(seeded.Expression, " == ")
	if !isEquality || left != right {
		t.Fatalf("the refuted suggestion is not the seeded tautology: %q", seeded.Expression)
	}

	if !seeded.Verification.Baseline.Passed {
		t.Fatal("the vacuous assertion should keep the suite green; it did not")
	}

	if seeded.Verification.Mutant.Passed {
		t.Fatal("the vacuous assertion was credited with a kill it did not make")
	}

	if len(withStatus(result, report.SuggestionVerified)) == 0 {
		t.Fatal("no real suggestion verified beside the seeded one, so nothing could have " +
			"laundered it and the attribution claim is untested")
	}
}

//nolint:paralleltest // owns package-global suggestion hook for its lifetime.
func TestASuggestExitCodeIsOneOnlyWhenSomethingIsRefuted(t *testing.T) {
	clean := runSuggest(t, suggestRequest(t, copyFixture(t, suggestBasicFixture)))
	if code := clean.ExitCode(report.Gate{}); code != report.ExitClean { // no gate.
		t.Fatalf("exit code = %d, want %d when every suggestion concluded",
			code, report.ExitClean)
	}

	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	engine.SetSuggestionDefectSeed(t, config.ModuleDir, suggest.DefectVacuous)

	refuted := runSuggest(t, config)
	if code := refuted.ExitCode(report.Gate{}); code != report.ExitFindings { // no gate.
		t.Fatalf("exit code = %d, want %d when a suggestion was refuted", code, report.ExitFindings)
	}
}

func TestAStaleSurvivorIdentifierIsAnOperationalFailureNamingIt(t *testing.T) {
	t.Parallel()

	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	config.SurvivorIDs = []string{"000000000000", "111111111111"}

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrSurvivorSelection) {
		t.Fatalf("error = %v, want a survivor-selection failure", err)
	}

	for _, id := range config.SurvivorIDs {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("the failure does not name %s: %v", id, err)
		}
	}
}

// TestASuggestionLegFailureWritesNoBaseline holds the dispatch order ADR-0005
// records: the suggestion leg reacts to the classified population before the
// acceptance gate does, and a leg that fails stops delivery, so the baseline
// writer never runs. The control proves the same request writes the baseline
// when the leg succeeds, so the absence is the ordering and not a dead writer.
func TestASuggestionLegFailureWritesNoBaseline(t *testing.T) {
	t.Parallel()

	control := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	control.WriteBaseline = true

	if _, err := engine.Run(t.Context(), control); err != nil {
		t.Fatalf("control suggest run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(control.ModuleDir, engine.DefaultBaselineName)); err != nil {
		t.Fatalf("the control run wrote no baseline, so the ordering claim is untested: %v", err)
	}

	stale := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	stale.WriteBaseline = true
	stale.SurvivorIDs = []string{"000000000000"}

	if _, err := engine.Run(t.Context(), stale); !errors.Is(err, engine.ErrSurvivorSelection) {
		t.Fatalf("error = %v, want a survivor-selection failure", err)
	}

	_, err := os.Stat(filepath.Join(stale.ModuleDir, engine.DefaultBaselineName))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed suggestion leg still reached the baseline writer: stat error = %v", err)
	}
}

func TestSurvivorSelectionScopesTheSuggestions(t *testing.T) {
	t.Parallel()

	// Two target files, so the unscoped run carries more than one collapsed
	// suggestion and scoping has something to remove.
	module := copyFixture(t, suggestMultiFixture)

	all := runSuggest(t, dryRunRequest(t, module))
	if len(all.Suggestions) < 2 {
		t.Fatalf("the fixture produced %d suggestions; the scoping claim needs more than one",
			len(all.Suggestions))
	}

	config := dryRunRequest(t, module)
	config.SurvivorIDs = []string{all.Suggestions[0].MutantID}

	scoped := runSuggest(t, config)
	if len(scoped.Suggestions) != 1 {
		t.Fatalf("scoped run produced %d suggestions, want 1", len(scoped.Suggestions))
	}

	if scoped.Suggestions[0].MutantID != all.Suggestions[0].MutantID {
		t.Fatalf("scoped run suggested for %s, want %s",
			scoped.Suggestions[0].MutantID, all.Suggestions[0].MutantID)
	}
}

// TestVerificationIsNeverCached: the second run replays mutant verdicts from
// the cache and still verifies every suggestion afresh, with its own evidence.
func TestVerificationIsNeverCached(t *testing.T) {
	t.Parallel()

	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))

	first := runSuggest(t, config)
	second := runSuggest(t, config)
	if second.Population.Cached == 0 {
		t.Fatal("the second run cached no verdicts, so the claim is untested")
	}

	if len(withStatus(second, report.SuggestionVerified)) !=
		len(withStatus(first, report.SuggestionVerified)) {
		t.Fatalf("the cached run verified %d suggestions, the fresh run %d",
			len(withStatus(second, report.SuggestionVerified)),
			len(withStatus(first, report.SuggestionVerified)))
	}

	for _, suggestion := range withStatus(second, report.SuggestionVerified) {
		if suggestion.Verification == nil || len(suggestion.Verification.Mutant.Runs) == 0 {
			t.Fatalf("suggestion %s carries no freshly executed evidence", suggestion.ID)
		}
	}
}

// TestAScopedSuggestRunStatesItsVerificationCost is US14's bounded-and-chosen
// half: the report states what the verification contract executed — one
// full-suite run per target file, one isolated run per candidate.
func TestAScopedSuggestRunStatesItsVerificationCost(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestBasicFixture)

	dry := runSuggest(t, dryRunRequest(t, module))
	if len(dry.Suggestions) == 0 {
		t.Fatal("no suggestions to scope to")
	}

	config := suggestRequest(t, module)
	config.SurvivorIDs = []string{dry.Suggestions[0].MutantID}

	result := runSuggest(t, config)

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "verification executed 1 full-suite run(s)") &&
			strings.Contains(warning, "1 isolated mutant run(s)") {
			return
		}
	}

	t.Fatalf("the scoped run does not state its verification cost: %v", result.Warnings)
}

func TestVerificationCountsOnlyStartedIsolatedRuns(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		script     string
		wantCost   string
		wantReason string
	}{
		"a truncated stream counts the started run": {
			script: "case \"$1\" in\n" +
				"  */verify-kill-*) terraform \"$@\" | { head -c 2; cat >/dev/null; }; exit ;;\n" +
				"esac\n",
			wantCost:   "verification executed 1 full-suite run(s) plus 5 isolated mutant run(s)",
			wantReason: "terraform output could not be decoded",
		},
		"a missing executable does not count a run": {
			script: "case \"$1\" in\n" +
				"  */verify-batch-*) terraform \"$@\"; status=$?; rm -- \"$0\"; exit \"$status\" ;;\n" +
				"esac\n",
			wantCost:   "verification executed 1 full-suite run(s) plus 0 isolated mutant run(s)",
			wantReason: "terraform could not be executed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := suggestRequest(t, copyFixture(t, suggestBasicFixture))
			wrapper := filepath.Join(t.TempDir(), "terraform-verification")
			script := "#!/usr/bin/env bash\n" + test.script + "exec terraform \"$@\"\n"
			testsupport.WriteExecutable(t, wrapper, script)

			request.TerraformBinary = wrapper
			result := runSuggest(t, request)
			refuted := withStatus(result, report.SuggestionRefuted)
			if len(refuted) != 1 || !strings.Contains(refuted[0].StatusReason, test.wantReason) {
				t.Fatalf("want one refutation naming %q, got %+v", test.wantReason, result.Suggestions)
			}

			if !slices.Contains(result.Warnings, test.wantCost) {
				t.Fatalf("want verification cost %q, got %v", test.wantCost, result.Warnings)
			}
		})
	}
}

func TestEmptyCollectionAbsenceFailsTheSuggestedAssertion(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		source string
		member string
	}{
		"array member": {member: ".labels"},
		"object member": {
			source: "output \"settings\" {\n  value = { labels = {} }\n}\n", member: ".labels",
		},
		"indexed element": {
			source: "output \"settings\" {\n  value = [[]]\n}\n", member: "[0]",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, suggestEmptyCollectionFixture)
			if test.source != "" {
				testsupport.WriteFile(t, filepath.Join(module, mainFile), test.source)
			}

			request := suggestRequest(t, module)
			request.IncludeOperators = []string{"COLL-EMPTY"}
			result := runSuggest(t, request)
			if len(result.Suggestions) != 1 {
				t.Fatalf("want one emptiness suggestion, got %+v", result.Suggestions)
			}

			suggestion := result.Suggestions[0]
			want := "try(length(output.settings" + test.member + "), null) == 0"
			if suggestion.Expression != want || suggestion.Status != report.SuggestionVerified ||
				suggestion.Verification == nil {
				t.Fatalf("want verified %s, got %+v", want, suggestion)
			}

			evidence := suggestion.Verification
			if !evidence.Baseline.Passed || !evidence.Mutant.Passed || len(evidence.Mutant.Runs) != 1 ||
				evidence.Mutant.Runs[0].Status != "fail" {
				t.Fatalf("absence must fail the assertion with a green baseline, got %+v", evidence)
			}
		})
	}
}

func TestCollapsedVerificationPreservesKillsAcrossExecutionFailures(t *testing.T) {
	t.Parallel()

	dry := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestCollapseCarrierFixture)))
	if len(dry.Suggestions) != 1 {
		t.Fatalf("want one collapsed candidate, got %+v", dry.Suggestions)
	}

	listed := append([]string{dry.Suggestions[0].MutantID}, dry.Suggestions[0].AlsoKills...)
	if len(listed) < 3 {
		t.Fatalf("want at least three members to check continuation after a failure, got %v", listed)
	}

	for name, failedIndex := range map[string]int{
		"carrier": 0, "middle": len(listed) / 2, "last": len(listed) - 1, "all": -1,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			condition := `[ "$2" = test ] && [[ "$1" == */verify-kill-* ]]`
			if failedIndex >= 0 {
				condition = `[ "$2" = test ] && [[ "$1" == */verify-kill-*-` + listed[failedIndex] + ` ]]`
			}

			request := suggestRequest(t, copyFixture(t, suggestCollapseCarrierFixture))
			request.TerraformBinary = sleepingTerraform(t, condition, 30)
			request.TimeoutFactor = 0.0001
			request.TimeoutFloor = 2 * time.Second
			result := runSuggest(t, request)
			if len(result.Suggestions) != 1 || result.Suggestions[0].Verification == nil {
				t.Fatalf("want one suggestion with evidence, got %+v", result.Suggestions)
			}

			suggestion := result.Suggestions[0]
			leg := suggestion.Verification.Mutant
			wantStatus := report.SuggestionVerified
			wantRuns := len(listed) - 1
			if failedIndex < 0 {
				wantStatus = report.SuggestionRefuted
				wantRuns = 0
			}

			if suggestion.Status != wantStatus || leg.Passed != (wantRuns > 0) ||
				!suggestion.Verification.Baseline.Passed || len(leg.Runs) != wantRuns {
				t.Fatalf("completed kills were lost or an incomplete leg proved a kill: %+v", suggestion)
			}

			assertCollapsedMutantOutcomes(t, leg.Detail, listed, failedIndex)
		})
	}
}

func assertCollapsedMutantOutcomes(t *testing.T, detail string, listed []string, failedIndex int) {
	t.Helper()

	for index, id := range listed {
		wantOutcome := "mutant " + id + " failed the suggested assertion"
		if failedIndex < 0 || index == failedIndex {
			wantOutcome = "mutant " + id + ": the isolated mutant leg could not be executed: " +
				"the verification suite did not finish within its budget"
		}

		if !strings.Contains(detail, wantOutcome) {
			t.Fatalf("missing member outcome %q in %q", wantOutcome, detail)
		}
	}
}

// TestSurvivorsSharingOneAssertionCollapseIntoOneSuggestion is the round-3
// review's duplication finding: five survivors one expression kills are one
// suggestion carrying the rest in also_kills, one applied assert block, and
// still one isolated verification per mutant.
func TestSurvivorsSharingOneAssertionCollapseIntoOneSuggestion(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestBasicFixture)

	config := suggestRequest(t, module)
	config.ApplyAll = true

	result := runSuggest(t, config)

	verified := withStatus(result, report.SuggestionVerified)
	if len(verified) != 1 {
		t.Fatalf("want one collapsed suggestion, got %d (%s)",
			len(verified), statusSummary(result.Suggestions))
	}

	claimed := 1 + len(verified[0].AlsoKills)
	if claimed < 2 {
		t.Fatalf("the fixture's shared assertion claims only %d mutant(s); the collapse is untested", claimed)
	}

	// One isolated run per claimed mutant, visible in the evidence and the
	// cost statement both.
	if got := len(verified[0].Verification.Mutant.Runs); got != claimed {
		t.Fatalf("isolated evidence covers %d run(s), want one per claimed mutant (%d)", got, claimed)
	}

	written := readFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"))
	if got := strings.Count(written, verified[0].Expression); got != 1 {
		t.Fatalf("the applied file carries the shared assertion %d times, want once", got)
	}
}

func TestADryRunRefusesAnApplySelection(t *testing.T) {
	t.Parallel()

	config := dryRunRequest(t, copyFixture(t, suggestBasicFixture))
	config.ApplyAll = true

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrSuggestCombination) {
		t.Fatalf("error = %v, want a contradictory-flags refusal", err)
	}
}

func TestSuggestRefusesATestSelection(t *testing.T) {
	t.Parallel()

	config := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	config.TestSelection = []string{"tests/unit.tftest.hcl"}

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrSuggestCombination) {
		t.Fatalf("error = %v, want a refusal: verification runs the full suite by contract", err)
	}
}

// TestABatchFailureIsRefutedOnlyWhereTheRunNamesIt is the batch-attribution
// proof: one target file carries several valid suggestions and one seeded
// assertion that is false on the baseline. The batch leg fails once, and the
// failing run names the false assertion — every applied assertion carries its
// suggestion's identifier in its error_message, and Terraform copies that
// message into the failing run's diagnostic — so only the named suggestion is
// refuted and the valid ones are verified.
//
//nolint:paralleltest // owns package-global suggestion hook for its lifetime.
func TestABatchFailureIsRefutedOnlyWhereTheRunNamesIt(t *testing.T) {
	config := suggestRequest(t, copyFixture(t, suggestBatchFixture))
	engine.SetSuggestionDefectSeed(t, config.ModuleDir, suggest.DefectWrongValue)

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) != 1 {
		t.Fatalf("want exactly the seeded wrong value refuted, got %s",
			statusSummary(result.Suggestions))
	}

	if !strings.Contains(refuted[0].Expression, suggest.SeededWrongValue) {
		t.Fatalf("the refuted suggestion is not the seeded wrong value: %q", refuted[0].Expression)
	}

	if refuted[0].StatusReason == "" {
		t.Fatal("the refuted suggestion carries no reason")
	}

	verified := withStatus(result, report.SuggestionVerified)
	if len(verified) != 2 {
		t.Fatalf("want both valid suggestions verified beside the refuted one, got %s",
			statusSummary(result.Suggestions))
	}

	for _, suggestion := range verified {
		if suggestion.Verification == nil || !suggestion.Verification.Baseline.Passed {
			t.Fatalf("verified suggestion %s does not carry a passed baseline leg", suggestion.ID)
		}
	}

	// The re-run the attribution spent is a run the report states: one initial
	// batch, one re-run without the refuted suggestion, and one isolated run
	// per claimed mutant. The seeded assertion replaces the first candidate,
	// whose remaining mutants collapse into a four-mutant suggestion beside
	// the five-mutant one; the refuted seed claims a mutant but never reaches
	// its isolated leg.
	wantCost := "verification executed 2 full-suite run(s) plus 9 isolated mutant run(s)"
	found := false
	for _, warning := range result.Warnings {
		if warning == wantCost {
			found = true
		}
	}

	if !found {
		t.Fatalf("the report does not state the attribution's actual cost %q: %v", wantCost, result.Warnings)
	}
}

// TestABatchFailureNamingNothingIsBisected is the attribution fallback: the
// seeded assertion references an output the baseline does not declare, so the
// run carrying it errors rather than fails and Terraform's diagnostic reports
// its own error, not the assertion's message — no diagnostic names a
// suggestion, and only splitting the batch can attribute the failure.
//
//nolint:paralleltest // owns package-global suggestion hook for its lifetime.
func TestABatchFailureNamingNothingIsBisected(t *testing.T) {
	config := suggestRequest(t, copyFixture(t, suggestBatchFixture))
	engine.SetSuggestionDefectSeed(t, config.ModuleDir, suggest.DefectVanishing)

	result := runSuggest(t, config)

	refuted := withStatus(result, report.SuggestionRefuted)
	if len(refuted) != 1 {
		t.Fatalf("want exactly the seeded vanishing suggestion refuted, got %s",
			statusSummary(result.Suggestions))
	}

	if !strings.Contains(refuted[0].Expression, suggest.SeededVanishingOutput) {
		t.Fatalf("the refuted suggestion is not the seeded vanishing one: %q", refuted[0].Expression)
	}

	if len(withStatus(result, report.SuggestionVerified)) != 2 {
		t.Fatalf("want both valid suggestions verified beside the refuted one, got %s",
			statusSummary(result.Suggestions))
	}

	// Bisection's cost is the runs it actually spent: the initial batch, the
	// failing singleton, and the green remainder — plus one isolated run per
	// claimed mutant, nine under the same collapse as the named path.
	wantCost := "verification executed 3 full-suite run(s) plus 9 isolated mutant run(s)"
	found := false
	for _, warning := range result.Warnings {
		if warning == wantCost {
			found = true
		}
	}

	if !found {
		t.Fatalf("the report does not state the bisection's actual cost %q: %v", wantCost, result.Warnings)
	}
}

// TestARemovedOutputElementFailsTheAssertionRatherThanErroring is the D14
// proof at the seam: the generated assertion indexes an output element its
// own mutant removes, and the try form turns the removal into a plain
// failure — every isolated leg fails, none errors, and the suggestion
// verifies under the StatusFail-only contract.
func TestARemovedOutputElementFailsTheAssertionRatherThanErroring(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestCountFixture)))

	found := false
	for _, suggestion := range result.Suggestions {
		if suggestion.Expression != `try(output.nodes[0], null) == "node"` {
			continue
		}

		found = true

		if suggestion.Verification == nil {
			t.Fatal("the verified suggestion carries no verification evidence")
		}

		if suggestion.Status != report.SuggestionVerified ||
			!suggestion.Verification.Baseline.Passed ||
			!suggestion.Verification.Mutant.Passed {
			t.Fatalf("want the baseline-green output-element assertion verified, got %+v", suggestion)
		}

		for _, run := range suggestion.Verification.Mutant.Runs {
			if run.Status != "fail" {
				t.Fatalf("a mutant leg run came back %s; the removed element must fail "+
					"the assertion, not error it", run.Status)
			}
		}
	}

	if !found {
		t.Fatalf("no suggestion carries the output-element assertion; statuses were %s",
			statusSummary(result.Suggestions))
	}

	if refuted := withStatus(result, report.SuggestionRefuted); len(refuted) != 0 {
		t.Fatalf("want nothing refuted where every removal fails plainly, got %s",
			statusSummary(result.Suggestions))
	}
}
