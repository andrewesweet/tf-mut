package report

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Exit codes the command line contract promises.
const (
	// ExitClean means the run completed with nothing to report.
	ExitClean = 0
	// ExitFindings means the run completed and found something worth failing on.
	ExitFindings = 1
	// ExitOperational means the run could not produce a trustworthy result.
	ExitOperational = 2
)

// Gate is the pass or fail policy applied to a completed report.
type Gate struct {
	// MinScore is the mutation score percentage required to pass.
	MinScore float64
	// HasMinScore reports whether a minimum score was requested.
	HasMinScore bool
	// AllowIncompleteScore permits a passing gate despite a timeout.
	AllowIncompleteScore bool
}

// ExitCode applies the gate to the report.
func (r Report) ExitCode(gate Gate) int {
	if r.Command == CommandPreview {
		return ExitClean
	}

	if len(r.Errors) > 0 {
		return ExitOperational
	}

	if gate.HasMinScore {
		if r.Metrics.Incomplete && !gate.AllowIncompleteScore {
			return ExitFindings
		}

		if r.Metrics.MutationScore*percent < gate.MinScore {
			return ExitFindings
		}

		return ExitClean
	}

	if r.Count(Survived) > 0 || len(r.Findings) > 0 {
		return ExitFindings
	}

	return ExitClean
}

const percent = 100.0

// WriteJSON renders the machine-readable report.
func WriteJSON(writer io.Writer, value Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encoding report: %w", err)
	}

	return nil
}

// WriteTerminal renders the human-readable report.
func WriteTerminal(writer io.Writer, value Report) error {
	builder := strings.Builder{}

	if value.Command == CommandPreview {
		writePreview(&builder, value)
	} else {
		writeRun(&builder, value)
	}

	if _, err := io.WriteString(writer, builder.String()); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	return nil
}

func writePreview(builder *strings.Builder, value Report) {
	fmt.Fprintf(builder, "tf-mut preview  %s\n\n", value.Module)
	fmt.Fprintf(builder, "  %d mutants would be generated\n\n", len(value.Mutants))

	for _, operator := range operatorCounts(value.Mutants) {
		fmt.Fprintf(builder, "  %-24s %d\n", operator.name, operator.count)
	}

	builder.WriteString("\n")

	for _, mutant := range value.Mutants {
		fmt.Fprintf(builder, "%s  %s  %s\n", mutant.Operator, location(mutant.Range), mutant.Site)
		writeDiff(builder, mutant.Diff)
	}

	writeWarnings(builder, value.Warnings)
}

func writeRun(builder *strings.Builder, value Report) {
	fmt.Fprintf(builder, "tf-mut run  %s\n\n", value.Module)

	writeFindings(builder, value)
	writeSurvivors(builder, value)
	writeMetrics(builder, value)
	writeErrors(builder, value.Errors)
	writeWarnings(builder, value.Warnings)
}

func writeFindings(builder *strings.Builder, value Report) {
	if len(value.Findings) == 0 {
		return
	}

	fmt.Fprintf(builder, "PSEUDO-TESTED RESOURCES (%d)\n", len(value.Findings))
	builder.WriteString("  Planned by a test, asserted on by nothing.\n\n")

	for _, finding := range value.Findings {
		fmt.Fprintf(builder, "  %-40s %s\n", finding.Address, location(finding.Range))
		fmt.Fprintf(builder, "    %s\n", finding.Message)
	}

	builder.WriteString("\n")
}

func writeSurvivors(builder *strings.Builder, value Report) {
	survivors := value.Survivors()
	if len(survivors) == 0 {
		return
	}

	fmt.Fprintf(builder, "SURVIVED (%d)\n\n", len(survivors))

	for _, mutant := range survivors {
		fmt.Fprintf(builder, "  %s  %s  %s\n", mutant.Operator, location(mutant.Range), mutant.Site)
		writeDiff(builder, mutant.Diff)
	}
}

func writeMetrics(builder *strings.Builder, value Report) {
	metrics := value.Metrics

	fmt.Fprintf(builder, "  Mutation score   %6.1f%%   (%d of %d scored mutants detected)\n",
		metrics.MutationScore*percent, value.Count(Killed)+value.Count(KilledByError), metrics.Scored)
	fmt.Fprintf(builder, "  Assertion score  %6.1f%%   (assertion kills only)\n", metrics.AssertionScore*percent)
	fmt.Fprintf(builder, "  Reachability     %6.1f%%\n\n", metrics.Reachability*percent)

	fmt.Fprintf(builder, "  Killed %d   KilledByError %d   Survived %d   NoCoverage %d   Timeout %d   Invalid %d\n",
		value.Count(Killed), value.Count(KilledByError), value.Count(Survived),
		value.Count(NoCoverage), value.Count(Timeout), value.Count(Invalid))

	if metrics.Incomplete {
		builder.WriteString("\n  Score is INCOMPLETE: at least one mutant timed out.\n")
	}
}

func writeErrors(builder *strings.Builder, errors []ExecutionError) {
	if len(errors) == 0 {
		return
	}

	fmt.Fprintf(builder, "\nOPERATIONAL FAILURES (%d)\n", len(errors))
	builder.WriteString("  These mutants produced no trustworthy verdict.\n")

	for _, failure := range errors {
		fmt.Fprintf(builder, "  %s  %s: %s\n", failure.MutantID, failure.Site, failure.Message)
	}
}

func writeWarnings(builder *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	builder.WriteString("\nWARNINGS\n")

	for _, warning := range warnings {
		fmt.Fprintf(builder, "  %s\n", warning)
	}
}

func writeDiff(builder *strings.Builder, diff string) {
	for line := range strings.SplitSeq(strings.TrimSuffix(diff, "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") {
			continue
		}

		fmt.Fprintf(builder, "    %s\n", strings.TrimRight(line, " \t"))
	}

	builder.WriteString("\n")
}

func location(source Range) string {
	return fmt.Sprintf("%s:%d", source.File, source.Start.Line)
}

type operatorCount struct {
	name  string
	count int
}

func operatorCounts(mutants []Mutant) []operatorCount {
	counts := map[string]int{}
	for _, mutant := range mutants {
		counts[mutant.Operator]++
	}

	ordered := make([]operatorCount, 0, len(counts))
	for name, count := range counts {
		ordered = append(ordered, operatorCount{name: name, count: count})
	}

	slices.SortFunc(ordered, func(left, right operatorCount) int {
		return strings.Compare(left.name, right.name)
	})

	return ordered
}
