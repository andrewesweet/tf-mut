package engine

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// extremeOperators are the Tier 0 operators whose survival makes a resource
// pseudo-tested.
//
//nolint:gochecknoglobals // an immutable lookup table.
var extremeOperators = map[string]bool{
	string(mutation.AttrDelete):     true,
	string(mutation.ResourceDelete): true,
	string(mutation.BodyBlank):      true,
}

// evidence accumulates what the extreme mutants of one resource showed.
type evidence struct {
	module        string
	address       string
	executed      int
	killed        int
	killedByError int
	timeouts      int
	mutants       []string
	site          report.Range
}

// findings derives the milestone's headline: resources planned by a test and
// asserted on by nothing.
//
// A KilledByError never counts as evidence of testing — Terraform's own
// "Invalid index" on an emptied instance set is not a test — and a timeout
// leaves the question open rather than answering it. The classified rows
// decide; the aligned published rows carry the identities the finding names.
func findingsOf(
	configuration discovery.Configuration,
	mutants []report.Mutant,
	population oracle.Population,
) []report.Finding {
	byResource := map[string]*evidence{}
	rows := population.Classified()

	for index, mutant := range mutants {
		if mutant.Resource == "" || !extremeOperators[mutant.Operator] {
			continue
		}

		key := mutant.Module + "|" + mutant.Resource

		entry, found := byResource[key]
		if !found {
			entry = &evidence{
				module:  mutant.Module,
				address: mutant.Resource,
				mutants: []string{},
				site:    mutant.Range,
			}
			byResource[key] = entry
		}

		entry.mutants = append(entry.mutants, mutant.ID)
		countEvidence(entry, rows[index].Outcome.State())
	}

	results := []report.Finding{}

	for _, entry := range byResource {
		if entry.executed == 0 || entry.killed > 0 || entry.timeouts > 0 {
			continue
		}

		results = append(results, report.Finding{
			ID:      mutation.FindingID(report.PseudoTested, entry.module, entry.address),
			Kind:    report.PseudoTested,
			Address: entry.address,
			Module:  entry.module,
			Range:   resourceRange(configuration, entry, entry.site),
			Message: describeEvidence(*entry),
			Mutants: entry.mutants,
		})
	}

	slices.SortFunc(results, func(left, right report.Finding) int {
		if left.Module != right.Module {
			return strings.Compare(left.Module, right.Module)
		}

		return strings.Compare(left.Address, right.Address)
	})

	return results
}

func countEvidence(entry *evidence, state oracle.State) {
	// Invalid, NoCoverage and Pending mutants never executed, so they are no
	// evidence either way.
	if state != oracle.StateKilled && state != oracle.StateKilledByError &&
		state != oracle.StateSurvived && state != oracle.StateTimeout {
		return
	}

	entry.executed++

	if state == oracle.StateKilled {
		entry.killed++
	}

	if state == oracle.StateKilledByError {
		entry.killedByError++
	}

	if state == oracle.StateTimeout {
		entry.timeouts++
	}
}

func describeEvidence(entry evidence) string {
	message := fmt.Sprintf("%d extreme mutant(s) executed and no assertion caught any of them",
		entry.executed)

	if entry.killedByError > 0 {
		message += fmt.Sprintf("; %d were caught only by Terraform's own evaluation, which is not a test",
			entry.killedByError)
	}

	return message + "."
}

// resourceRange prefers the resource block's own declaration range over the
// range of whichever mutant happened to be seen first.
func resourceRange(configuration discovery.Configuration, entry *evidence, fallback report.Range) report.Range {
	module, found := configuration.ModuleByRel(entry.module)
	if !found {
		return fallback
	}

	for _, resource := range module.Resources {
		if resource.Address != entry.address {
			continue
		}

		file := resource.File

		if relative, err := filepath.Rel(configuration.ClosureRoot, resource.File); err == nil {
			file = filepath.ToSlash(relative)
		}

		return report.Range{
			File:  file,
			Start: report.Position{Line: resource.DefRange.Start.Line, Column: resource.DefRange.Start.Column},
			End:   report.Position{Line: resource.DefRange.End.Line, Column: resource.DefRange.End.Column},
		}
	}

	return fallback
}

// unanswerableResources names resources no enabled operator fired on at all, so
// that silence is never mistaken for a verdict.
//
// The definition is over every enabled tier rather than over Tier 0 alone: with
// the expression, collection and contract operators in the population, a
// resource whose every argument is schema-required is usually still reachable
// through its own expressions, and warning about it would be noise. What the
// warning still catches is the resource this run genuinely says nothing about.
func unanswerableResources(configuration discovery.Configuration, mutants []report.Mutant) []string {
	covered := map[string]bool{}

	for _, mutant := range mutants {
		if mutant.Resource != "" {
			covered[mutant.Module+"|"+mutant.Resource] = true
		}
	}

	root := configuration.RootRelative()
	missing := []string{}

	for _, module := range mutation.SurfaceModules(configuration, configuration.Tests.Dir) {
		// Helper modules under the test directory are the suite's scaffolding,
		// which the mutation surface already excludes; warning about the
		// resources their operators never reach would grade the fixture, not
		// the module. The exclusion is the surface's own rule.
		for _, resource := range module.Resources {
			if covered[module.Rel+"|"+resource.Address] {
				continue
			}

			// A bare address cannot tell two same-named resources in
			// different modules apart, so every non-root entry carries its
			// module's closure-relative path.
			address := resource.Address
			if module.Rel != root {
				address = module.Rel + "/" + resource.Address
			}

			missing = append(missing, address)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	slices.Sort(missing)

	return []string{fmt.Sprintf(
		"%d resource(s) have no mutant under any enabled operator, "+
			"so this run says nothing about them: %s",
		len(missing), strings.Join(missing, ", "),
	)}
}

// tripWarnings names the trip and the failure that started the streak, so a
// reader with a dead dependency learns it from one line instead of a page of
// identical failures.
func tripWarnings(trip *tripOutcome) []string {
	if trip == nil {
		return nil
	}

	return []string{fmt.Sprintf(
		"run tripped after %d consecutive operational failures; %d mutant(s) were left "+
			"unexecuted and the mutation score is marked incomplete. First failure: %s.",
		trip.consecutiveFailures, trip.unexecuted, trip.firstFailure,
	)}
}

// unscoredWarnings turns the population arithmetic the metrics already
// decided into warnings a reader sees without opening the counts: a run
// whose non-empty population produced no score at all, and a run whose
// Invalid share dominates. Both mean the run is saying something about the
// module or the tool, not about the tests.
func unscoredWarnings(population oracle.Population, metrics report.Metrics) []string {
	judged := len(population.Classified()) - metrics.Counts[report.Ignored]
	invalid := metrics.Counts[report.Invalid]

	warnings := []string{}

	if metrics.Scored == 0 && len(population.Classified()) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"none of the %d mutants in the population could be scored, so the "+
				"mutation score is not trustworthy and the report is marked incomplete",
			len(population.Classified()),
		))
	}

	if judged > 0 && float64(invalid)/float64(judged) >= oracle.InvalidShareWarningThreshold {
		dominant := dominantInvalidDiagnostic(population)
		diagnostic := ""

		if dominant != "" {
			diagnostic = fmt.Sprintf("; the most common diagnostic was %q", dominant)
		}

		warnings = append(warnings, fmt.Sprintf(
			"%d of %d mutants judged (%.0f%%) were rejected by terraform validate%s. "+
				"This points at a systematic mismatch between the enabled operators and "+
				"the module, not at the tests.",
			invalid, judged, 100*float64(invalid)/float64(judged), diagnostic,
		))
	}

	return warnings
}

// dominantInvalidDiagnostic returns the most common first-diagnostic summary
// across the population's Invalid outcomes, ties broken lexicographically so
// the warning is deterministic. An Invalid outcome with no diagnostics at all
// contributes nothing; a population with none returns the empty string.
func dominantInvalidDiagnostic(population oracle.Population) string {
	tally := map[string]int{}

	for _, row := range population.Classified() {
		if row.Outcome.State() != oracle.StateInvalid {
			continue
		}

		diagnostics := row.Outcome.Diagnostics()
		if len(diagnostics) == 0 {
			continue
		}

		tally[diagnostics[0].Summary]++
	}

	summaries := slices.Sorted(maps.Keys(tally))

	best, bestCount := "", 0

	for _, summary := range summaries {
		if tally[summary] > bestCount {
			best, bestCount = summary, tally[summary]
		}
	}

	return best
}

func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}
