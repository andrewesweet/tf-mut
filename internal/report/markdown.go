package report

import (
	"fmt"
	"io"
	"strings"
)

// M3d.1 (#51): the markdown reporter — the PR summary. Scores with the
// killed and killed-by-error split, the gate-table outcome, the new-versus-
// accepted survivor split, and a capped finding list.

// markdownFindingCap bounds the finding list a comment carries.
const markdownFindingCap = 10

// baselineNew is the provenance label the baseline gate assigns.
const baselineNew = "new"

// WriteMarkdown renders the PR summary.
func WriteMarkdown(writer io.Writer, value Report) error {
	page := strings.Builder{}

	page.WriteString("## tf-mut\n\n")
	fmt.Fprintf(&page,
		"**Mutation score %.1f%%** (killed %d + killed-by-error %d of %d scored) · "+
			"assertion score %.1f%% · reachability %.1f%%\n\n",
		value.Metrics.MutationScore*percent,
		value.Count(Killed), value.Count(KilledByError), value.Metrics.Scored,
		value.Metrics.AssertionScore*percent,
		value.Metrics.Reachability*percent)

	if value.Metrics.Incomplete {
		page.WriteString("> **Incomplete:** a timeout made this score untrustworthy.\n\n")
	}

	writePopulation(&page, value)
	writeGates(&page, value)
	writeFindingList(&page, value)
	writeSuggestionSummary(&page, value)

	if _, err := io.WriteString(writer, page.String()); err != nil {
		return fmt.Errorf("writing markdown report: %w", err)
	}

	return nil
}

// writeSuggestionSummary lists the outcome table in the job step summary. It
// carries no value a skipped-sensitive suggestion refused to render, because
// the step summary is one of the artefacts that rule names.
func writeSuggestionSummary(page *strings.Builder, value Report) {
	if len(value.Suggestions) == 0 {
		return
	}

	fmt.Fprintf(page, "\n### Suggested assertions (%d)\n\n", len(value.Suggestions))
	page.WriteString("| Suggestion | Status | Target | Condition |\n")
	page.WriteString("| --- | --- | --- | --- |\n")

	for index, suggestion := range value.Suggestions {
		if index >= markdownFindingCap {
			fmt.Fprintf(page, "\n… and %d more.\n", len(value.Suggestions)-markdownFindingCap)

			break
		}

		condition := "—"
		if suggestion.Expression != "" {
			condition = "`" + suggestion.Expression + "`"
		}

		fmt.Fprintf(page, "| `%s` | %s | `%s:%s` | %s |\n",
			suggestion.ID, suggestion.Status,
			suggestion.TargetFile, suggestion.TargetRun, condition)
	}

	page.WriteString("\n")
}

func writePopulation(page *strings.Builder, value Report) {
	fmt.Fprintf(page, "Population: %d selected, %d omitted, %d cached, %d fresh",
		value.Population.Selected, value.Population.Omitted,
		value.Population.Cached, value.Population.Fresh)

	if value.Selection.Mode == SelectionSince {
		fmt.Fprintf(page, " (`--since %s`", value.Selection.Ref)

		if value.Selection.ForcedFull != "" {
			fmt.Fprintf(page, "; full population forced: %s", value.Selection.ForcedFull)
		}

		page.WriteString(")")
	}

	if value.Sampling != nil {
		fmt.Fprintf(page, " — sampled %.0f%% with seed %d, **not authoritative**",
			value.Sampling.RatePercent, value.Sampling.Seed)
	}

	page.WriteString("\n\n")
}

// writeGates renders the gate-table outcome.
func writeGates(page *strings.Builder, value Report) {
	if value.Gates == nil {
		return
	}

	page.WriteString("| Gate | Outcome |\n| --- | --- |\n")

	if value.Gates.MinScore.Evaluated {
		fmt.Fprintf(page, "| `--min-score` | %s over the %s population%s |\n",
			passLabel(value.Gates.MinScore.Passed), value.Gates.MinScore.Scope,
			partialLabel(value.Gates.MinScore.Partial))
	}

	if value.Gates.FailOnNew.Evaluated {
		fmt.Fprintf(page, "| `--fail-on-new` | %s over the %s population%s |\n",
			passLabel(value.Gates.FailOnNew.Passed), value.Gates.FailOnNew.Scope,
			partialLabel(value.Gates.FailOnNew.Partial))
	}

	if baseline := value.Gates.Baseline; baseline != nil {
		fmt.Fprintf(page, "| Baseline | %d accepted, %d matched, **%d new**",
			baseline.Accepted, baseline.Matched, len(baseline.New))

		if baseline.StalenessReported {
			fmt.Fprintf(page, ", %d stale", len(baseline.Stale))
		} else {
			fmt.Fprintf(page, ", %d unobserved (staleness needs a full population)",
				len(baseline.Unobserved))
		}

		page.WriteString(" |\n")
	}

	page.WriteString("\n")
}

// writeFindingList renders the capped finding list, new before accepted.
func writeFindingList(page *strings.Builder, value Report) {
	findings := []Mutant{}

	for _, mutant := range value.Mutants {
		if mutant.State == Survived || mutant.State == StructurallyUnassertable {
			findings = append(findings, mutant)
		}
	}

	if len(findings) == 0 {
		page.WriteString("No surviving mutants.\n")

		return
	}

	fmt.Fprintf(page, "### Findings (%d", len(findings))

	newCount := 0

	for _, finding := range findings {
		if finding.Provenance != nil && finding.Provenance.BaselineStatus == baselineNew {
			newCount++
		}
	}

	if newCount > 0 || hasBaseline(value) {
		fmt.Fprintf(page, "; %d new, %d accepted", newCount, len(findings)-newCount)
	}

	page.WriteString(")\n\n")

	shown := 0

	for _, pass := range []string{baselineNew, ""} {
		for _, finding := range findings {
			status := ""
			if finding.Provenance != nil {
				status = finding.Provenance.BaselineStatus
			}

			if (pass == baselineNew) != (status == baselineNew) {
				continue
			}

			if shown >= markdownFindingCap {
				fmt.Fprintf(page, "- … and %d more\n", len(findings)-shown)

				return
			}

			writeFindingLine(page, finding, status)
			shown++
		}
	}
}

func writeFindingLine(page *strings.Builder, finding Mutant, status string) {
	label := ""
	if status != "" {
		label = " (" + status + ")"
	}

	diagnosis := ""
	if finding.Verdict != nil && finding.Verdict.Diagnosis != "" {
		diagnosis = " — " + string(finding.Verdict.Diagnosis)
	}

	fmt.Fprintf(page, "- `%s` %s at `%s`%s%s\n",
		finding.Operator, string(finding.State), finding.Site, diagnosis, label)
}

func hasBaseline(value Report) bool {
	return value.Gates != nil && value.Gates.Baseline != nil
}

func passLabel(passed bool) string {
	if passed {
		return "**passed**"
	}

	return "**failed**"
}

func partialLabel(partial bool) string {
	if partial {
		return ", labelled partial"
	}

	return ""
}
