package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
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
// leaves the question open rather than answering it.
func findings(configuration discovery.Configuration, mutants []report.Mutant) []report.Finding {
	byResource := map[string]*evidence{}

	for _, mutant := range mutants {
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
		countEvidence(entry, mutant.State)
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

func countEvidence(entry *evidence, state report.State) {
	// Invalid, NoCoverage and Pending mutants never executed, so they are no
	// evidence either way.
	if state != report.Killed && state != report.KilledByError &&
		state != report.Survived && state != report.Timeout {
		return
	}

	entry.executed++

	if state == report.Killed {
		entry.killed++
	}

	if state == report.KilledByError {
		entry.killedByError++
	}

	if state == report.Timeout {
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

	missing := []string{}

	for _, module := range configuration.Modules {
		for _, resource := range module.Resources {
			if covered[module.Rel+"|"+resource.Address] {
				continue
			}

			missing = append(missing, resource.Address)
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

func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}
