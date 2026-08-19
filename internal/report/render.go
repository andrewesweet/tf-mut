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
	// FailOnNew fails on any finding the baseline does not accept.
	FailOnNew bool
}

// characterisationExit applies the generation direction's own contract.
func (r Report) characterisationExit() int {
	// A commit that changed the tree and then stopped is an operational
	// failure, whatever the scaffold itself reported: the caller has a
	// half-written suite and has to act before anything else is true.
	if write := r.Characterisation.Write; write != nil && len(write.Partial) > 0 {
		return ExitOperational
	}

	// Incompleteness is a user-action state like any other: a rung that
	// pinned nothing has produced a suite nobody should trust, and exit 0
	// is reserved for output that is complete.
	if !r.Characterisation.Complete ||
		r.Characterisation.OpenTodos() > 0 ||
		len(r.Characterisation.Findings) > 0 {
		return ExitFindings
	}

	return ExitClean
}

// ExitCode applies the gate to the report. Requested gates compose: where
// both --min-score and --fail-on-new are given, both must pass.
func (r Report) ExitCode(gate Gate) int {
	if r.Command == CommandPreview {
		return ExitClean
	}

	if len(r.Errors) > 0 {
		return ExitOperational
	}

	// Characterisation has its own contract, and it is about work outstanding
	// rather than about a score: zero once the suite is complete, one while a
	// judgement point is still open or a curate finding is still unread, and
	// two only for an operational failure.
	if r.Characterisation != nil && r.Command != CommandCurate {
		return r.characterisationExit()
	}

	// `curate` carries a characterisation block and is nonetheless an ordinary
	// grading command: it executes a full, unsampled population — the posture
	// `checkCuratePopulation` enforces precisely so the numbers are worth
	// gating on — so `--min-score` and `--fail-on-new` compose for it exactly
	// as they do everywhere else. Keying the branch above on the block rather
	// than on the command meant `tf-mut curate --min-score 80` exited 0 on a
	// module scoring 10.
	if r.Command == CommandCurate && len(r.Characterisation.Findings) > 0 {
		return ExitFindings
	}

	// `suggest` has its own contract: zero once all the requested work has
	// concluded, whatever mix of verified and skipped it concluded in, and one
	// where any suggestion was refuted — a refutation is a generator defect and
	// a visible tool finding, not a survivor count.
	if r.Command == CommandSuggest {
		if r.Apply != nil && r.Apply.Aborted != "" {
			return ExitOperational
		}

		if r.SuggestionsByStatus()[SuggestionRefuted] > 0 {
			return ExitFindings
		}

		return ExitClean
	}

	if gate.HasMinScore || gate.FailOnNew {
		if gate.HasMinScore && !r.minScorePasses(gate) {
			return ExitFindings
		}

		if gate.FailOnNew && r.NewFindings() > 0 {
			return ExitFindings
		}

		return ExitClean
	}

	if r.Count(Survived) > 0 || len(r.Findings) > 0 {
		return ExitFindings
	}

	return ExitClean
}

// minScorePasses applies the score gate. The incomplete-score marker is never
// suppressed by anything: a baseline cannot make a timeout-affected score
// trustworthy.
func (r Report) minScorePasses(gate Gate) bool {
	if r.Metrics.Incomplete && !gate.AllowIncompleteScore {
		return false
	}

	return r.Metrics.MutationScore*percent >= gate.MinScore
}

// NewFindings counts the findings the baseline gate judged new.
func (r Report) NewFindings() int {
	if r.Gates == nil || r.Gates.Baseline == nil {
		return 0
	}

	return len(r.Gates.Baseline.New)
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

	switch {
	case value.Command == CommandPreview:
		writePreview(&builder, value)
	case value.Characterisation != nil:
		writeCharacterisation(&builder, value)
	default:
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
	fmt.Fprintf(builder, "tf-mut %s  %s\n\n", value.Command, value.Module)

	writeFindings(builder, value)
	writeSurvivors(builder, value)
	writeUnassertable(builder, value)
	writeSuggestions(builder, value)
	writeMetrics(builder, value)
	writeSuppressions(builder, value.Suppressions)
	writeErrors(builder, value.Errors)
	writeWarnings(builder, value.Warnings)
}

// writeSuggestions renders the outcome table as a review list: what the tool
// would write, what it proved, and what it refused to claim.
//
// A skipped suggestion prints its status and its reason and nothing else. That
// is not brevity: `skipped-sensitive` means the value must appear in no
// artefact, and the terminal is one of them.
func writeSuggestions(builder *strings.Builder, value Report) {
	if len(value.Suggestions) == 0 {
		return
	}

	fmt.Fprintf(builder, "SUGGESTED ASSERTIONS (%d)\n\n", len(value.Suggestions))

	for _, suggestion := range value.Suggestions {
		fmt.Fprintf(builder, "  %s  %s  %s:%s  (mutant %s)\n",
			suggestion.ID, suggestion.Status,
			suggestion.TargetFile, suggestion.TargetRun, suggestion.MutantID)

		if suggestion.Status.Skipped() {
			fmt.Fprintf(builder, "    %s\n\n", suggestion.StatusReason)

			continue
		}

		fmt.Fprintf(builder, "    condition = %s\n", suggestion.Expression)

		if suggestion.StatusReason != "" {
			fmt.Fprintf(builder, "    %s\n", suggestion.StatusReason)
		}

		writeDiff(builder, suggestion.Patch)
		builder.WriteString("\n")
	}

	writeApply(builder, value.Apply)
}

func writeApply(builder *strings.Builder, applied *AppliedSuggestions) {
	if applied == nil {
		return
	}

	if applied.Aborted != "" {
		fmt.Fprintf(builder, "  APPLY ABORTED: %s\n", applied.Aborted)

		if applied.Partial {
			fmt.Fprintf(builder, "  Written before the failure: %s\n",
				strings.Join(applied.Written, ", "))
			fmt.Fprintf(builder, "  Left unwritten: %s\n", strings.Join(applied.Pending, ", "))
			builder.WriteString("  Re-run `tf-mut suggest` to re-verify against the tree as it now is.\n")
		} else {
			builder.WriteString("  Nothing was written.\n")
		}

		builder.WriteString("\n")

		return
	}

	fmt.Fprintf(builder, "  Applied %d suggestion(s) to %s\n\n",
		len(applied.Requested), strings.Join(applied.Written, ", "))
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

		fmt.Fprintf(builder, "      %s   %s -> %s\n",
			changeLabel(change), rendered(change.Baseline), rendered(change.Mutant))
	}

	fmt.Fprintf(builder, "    Fix: %s.\n\n", verdict.Fix)
}

// shownChanges bounds the delta the terminal prints. Set from measured
// survivor data (M3c, docs/research/09-m3-real-provider-gate.md): the median
// real survivor delta is exactly three changes; the whole delta is in the
// JSON report.
const shownChanges = 3

// changeLabel names a change the way a reader would look for it: the Terraform
// address, extended by the payload member where the address alone would not
// distinguish two changes — an output's value, its type and its sensitivity all
// belong to one address.
func changeLabel(change Change) string {
	if change.Address == "" {
		return change.Path
	}

	segments := strings.Split(change.Path, ".")

	last := segments[len(segments)-1]
	if last == "" || strings.HasSuffix(change.Address, "."+last) || change.Address == last {
		return change.Address
	}

	return change.Address + "." + last
}

// rendered shows an absent value as absent rather than as nothing at all.
func rendered(value string) string {
	if value == "" {
		return "(absent)"
	}

	return value
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

// writeCharacterisation renders the scaffold, its pins and — where the caller
// did not ask for a write — the generated content itself, with delimiters.
//
// Printing the files is the default because there is nothing else useful to
// do with a suite the caller has not asked to have written: the alternative
// is a report about bytes the reader cannot see. A JSON reporter owning
// standard output takes the same content through the report instead.
func writeCharacterisation(builder *strings.Builder, value Report) {
	block := value.Characterisation

	fmt.Fprintf(builder, "tf-mut %s  %s\n\n", value.Command, value.Module)
	fmt.Fprintf(builder, "  granularity  %s\n", block.Rung)

	if block.Escalated {
		fmt.Fprintf(builder, "  escalated    %s\n", block.EscalationReason)
	}

	fmt.Fprintf(builder, "  scenarios    %d\n", len(block.Scenarios))
	writePinCounts(builder, block)

	if open := block.OpenTodos(); open > 0 {
		fmt.Fprintf(builder, "  open todos   %d — answer them, then run "+
			"tf-mut characterise --resume\n", open)
	}

	// Incompleteness has three causes and the report already knows which one
	// applies. Naming the granularity unconditionally told a reader with an
	// open judgement point — two lines after being told to answer it — that
	// their rung pinned nothing, which is false and points at the wrong remedy.
	if !block.Complete {
		builder.WriteString("  incomplete   " + incompleteReason(block) + "\n")
	}

	writeCurateFindings(builder, block)
	writeGeneratedFiles(builder, block)
	writeWarnings(builder, value.Warnings)
}

// incompleteReason names the cause the block records, rather than the one
// cause that happened to be written down first.
func incompleteReason(block *Characterisation) string {
	if block.OpenTodos() > 0 {
		return "a judgement point is still open"
	}

	if block.Convergence != nil && block.Convergence.StopReason == "refused" {
		return "the until-dry loop was refused: see the warning below"
	}

	return "the selected granularity produced no pins"
}

func writePinCounts(builder *strings.Builder, block *Characterisation) {
	counts := block.PinsByStatus()

	fmt.Fprintf(builder, "  pinned       %d\n", counts[Pinned])

	for _, status := range []PinStatus{
		PinSkippedVolatile, PinSkippedSensitive, PinSkippedUnrenderable, PinSkippedMockInvented,
	} {
		if counts[status] > 0 {
			fmt.Fprintf(builder, "  %-12s %d\n", status, counts[status])
		}
	}
}

func writeCurateFindings(builder *strings.Builder, block *Characterisation) {
	if len(block.Findings) == 0 {
		return
	}

	builder.WriteString("\n")

	for _, finding := range block.Findings {
		fmt.Fprintf(builder, "  %s  %s  %s\n", finding.ID, finding.Kind, finding.Message)
	}
}

func writeGeneratedFiles(builder *strings.Builder, block *Characterisation) {
	if block.Write != nil && block.Write.Requested {
		builder.WriteString("\n")

		for _, path := range block.Write.Written {
			fmt.Fprintf(builder, "  wrote %s\n", path)
		}

		if block.Write.Refused != "" {
			fmt.Fprintf(builder, "  refused: %s\n", block.Write.Refused)
		}

		return
	}

	for _, file := range block.Files {
		fmt.Fprintf(builder, "\n==> %s <==\n%s", file.Path, file.Content)
	}
}
