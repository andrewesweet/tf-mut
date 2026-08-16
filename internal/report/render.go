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
	writeUnassertable(builder, value)
	writeMetrics(builder, value)
	writeSuppressions(builder, value.Suppressions)
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

// writeSurvivors renders the run as a to-do list: every survivor names its
// diagnosis and the change that would resolve it, which is the difference
// between a tool that generates work and one that generates insight.
func writeSurvivors(builder *strings.Builder, value Report) {
	survivors := value.Survivors()
	if len(survivors) == 0 {
		return
	}

	fmt.Fprintf(builder, "SURVIVED (%d)\n\n", len(survivors))

	for _, mutant := range survivors {
		fmt.Fprintf(builder, "  %s  %s  %s\n", mutant.Operator, location(mutant.Range), mutant.Site)
		writeDiff(builder, mutant.Diff)
		writeVerdict(builder, mutant.Verdict)
	}
}

func writeUnassertable(builder *strings.Builder, value Report) {
	unassertable := []Mutant{}

	for _, mutant := range value.Mutants {
		if mutant.State == StructurallyUnassertable {
			unassertable = append(unassertable, mutant)
		}
	}

	if len(unassertable) == 0 {
		return
	}

	fmt.Fprintf(builder, "STRUCTURALLY UNASSERTABLE (%d)\n", len(unassertable))
	builder.WriteString("  No plan or state projection, so no assertion could ever catch these.\n\n")

	for _, mutant := range unassertable {
		fmt.Fprintf(builder, "  %s  %s  %s\n", mutant.Operator, location(mutant.Range), mutant.Site)
		writeVerdict(builder, mutant.Verdict)
	}
}

func writeVerdict(builder *strings.Builder, verdict *Verdict) {
	if verdict == nil {
		return
	}

	if verdict.Diagnosis != "" {
		fmt.Fprintf(builder, "    Diagnosis: %s\n", verdict.Diagnosis)
	}

	fmt.Fprintf(builder, "    %s.\n", verdict.Message)

	for index, change := range verdict.Evidence.Delta {
		if index >= shownChanges {
			fmt.Fprintf(builder, "      ... and %d more\n", len(verdict.Evidence.Delta)-shownChanges)

			break
		}

		fmt.Fprintf(builder, "      %s   %s -> %s\n", changeLabel(change), change.Baseline, change.Mutant)
	}

	fmt.Fprintf(builder, "    Fix: %s.\n\n", verdict.Fix)
}

// shownChanges bounds the delta the terminal prints. The whole delta is in the
// JSON report; a screen of it helps nobody.
const shownChanges = 3

func changeLabel(change Change) string {
	if change.Address != "" {
		return change.Address
	}

	return change.Path
}

func writeSuppressions(builder *strings.Builder, suppressions []Suppression) {
	rejected := []Suppression{}

	for _, suppression := range suppressions {
		if !suppression.Accepted {
			rejected = append(rejected, suppression)
		}
	}

	if len(rejected) == 0 {
		return
	}

	fmt.Fprintf(builder, "\nREJECTED SUPPRESSIONS (%d)\n", len(rejected))
	builder.WriteString("  These directives suppressed nothing, so the findings stand.\n")

	for _, suppression := range rejected {
		where := ""
		if suppression.Range != nil {
			where = location(*suppression.Range) + "  "
		}

		fmt.Fprintf(builder, "  %s%s: %s\n", where,
			strings.Join(suppression.Operators, ","), suppression.Rejection)
	}
}

func writeMetrics(builder *strings.Builder, value Report) {
	metrics := value.Metrics

	fmt.Fprintf(builder, "  Mutation score   %6.1f%%   (%d of %d scored mutants detected)\n",
		metrics.MutationScore*percent, value.Count(Killed)+value.Count(KilledByError), metrics.Scored)
	fmt.Fprintf(builder, "  Assertion score  %6.1f%%   (assertion kills only)\n", metrics.AssertionScore*percent)
	fmt.Fprintf(builder, "  Reachability     %6.1f%%\n\n", metrics.Reachability*percent)

	fmt.Fprintf(builder, "  Killed %d   KilledByError %d   Survived %d   Unassertable %d   NoCoverage %d\n",
		value.Count(Killed), value.Count(KilledByError), value.Count(Survived),
		value.Count(StructurallyUnassertable), value.Count(NoCoverage))
	fmt.Fprintf(builder, "  Timeout %d   Invalid %d   Unobservable %d   Ignored %d\n",
		value.Count(Timeout), value.Count(Invalid),
		value.Count(Unobservable), value.Count(Ignored))

	writeDiagnoses(builder, value)

	if metrics.Incomplete {
		builder.WriteString("\n  Score is INCOMPLETE: at least one mutant timed out.\n")
	}
}

// writeDiagnoses shows how the survivors divide, because "twenty survivors" and
// "twenty survivors, eighteen of them indeterminate" are different situations.
func writeDiagnoses(builder *strings.Builder, value Report) {
	if len(value.Metrics.Diagnoses) == 0 {
		return
	}

	names := make([]string, 0, len(value.Metrics.Diagnoses))
	for diagnosis := range value.Metrics.Diagnoses {
		names = append(names, string(diagnosis))
	}

	slices.Sort(names)

	builder.WriteString("\n  Survivor diagnoses:")

	for _, name := range names {
		fmt.Fprintf(builder, "   %s %d", name, value.Metrics.Diagnoses[Diagnosis(name)])
	}

	builder.WriteString("\n")
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
