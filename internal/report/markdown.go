package report

import (
	"fmt"
	"html"
	"io"
	"strings"
)

func markdownText(value string) string {
	escaped := strings.Builder{}

	for _, character := range value {
		if strings.ContainsRune("\\`*_[]()#+-.!~|", character) {
			escaped.WriteByte('\\')
		}

		escaped.WriteRune(character)
	}

	return html.EscapeString(escaped.String())
}

func markdownCode(value string) string {
	size, run := 1, 0

	for _, character := range value {
		if character != '`' {
			run = 0

			continue
		}

		run++
		size = max(size, run+1)
	}

	delimiter := strings.Repeat("`", size)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		value = " " + value + " "
	}

	return delimiter + value + delimiter
}

func markdownTableCode(value string) string {
	return strings.ReplaceAll(markdownCode(value), "|", "\\|")
}

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

	groups, rest := partitionIndeterminate(findings)
	writeSiteGroups(page, groupBySite(rest))
	writeIndeterminateGroups(page, groups)
}

// writeIndeterminateGroups renders the indeterminate survivors collapsed by
// their shared unknown paths: N survivors are indeterminate because these
// paths are unknown at plan, said once per path set instead of once per
// survivor. The fix text is shared by the whole group, so it is stated once
// too.
func writeIndeterminateGroups(page *strings.Builder, groups []indeterminateGroup) {
	if len(groups) == 0 {
		return
	}

	page.WriteString("#### Indeterminate at plan\n\n")

	for _, group := range groups {
		if len(group.members) == 1 {
			page.WriteString("**1 survivor** is indeterminate because these paths are unknown at plan:\n\n")
		} else {
			fmt.Fprintf(page, "**%d survivors** are indeterminate because these paths are unknown at plan:\n\n",
				len(group.members))
		}

		for _, path := range group.paths {
			fmt.Fprintf(page, "- %s\n", markdownCode(path))
		}

		fix := ""
		if verdict := group.members[0].Verdict; verdict != nil {
			fix = verdict.Fix
		}

		fmt.Fprintf(page, "\nFix: %s\n\n", markdownText(fix))

		for _, site := range groupBySite(group.members) {
			fmt.Fprintf(page, "##### %s — %s\n\n",
				markdownCode(site.site), markdownText(location(site.members[0].Range)))

			for _, member := range site.members {
				status := baselineStatusLabel(member)
				if status != "" {
					status = " (" + markdownText(status) + ")"
				}

				fmt.Fprintf(page, "- %s %s at %s%s\n",
					markdownCode(member.Operator), markdownText(string(member.State)), markdownText(location(member.Range)), status)
			}

			page.WriteString("\n")
		}
	}
}

// siteGroup is the findings at one module-local mutation site, in report order.
type siteGroup struct {
	site    string
	members []Mutant
}

// groupBySite partitions findings by module and mutation site, preserving
// first appearance in report order for groups and members.
func groupBySite(findings []Mutant) []siteGroup {
	groups := []siteGroup{}
	index := map[[2]string]int{}

	for _, finding := range findings {
		key := [2]string{finding.Module, finding.Site}
		at, found := index[key]
		if !found {
			at = len(groups)
			index[key] = at
			groups = append(groups, siteGroup{site: finding.Site})
		}

		groups[at].members = append(groups[at].members, finding)
	}

	return groups
}

// writeSiteGroups renders module-local sites in full up to markdownSiteCap.
// Overflow names and counts point readers to JSON for individual findings
// and locations.
func writeSiteGroups(page *strings.Builder, groups []siteGroup) {
	for shown, group := range groups {
		if shown >= markdownSiteCap {
			fmt.Fprintf(page,
				"- … and %d more sites (their findings are in the JSON report):\n",
				len(groups)-markdownSiteCap)

			for _, hidden := range groups[markdownSiteCap:] {
				fmt.Fprintf(page, "  - %s — %s\n", markdownCode(hidden.site), markdownText(findingCountLabel(len(hidden.members))))
			}

			return
		}

		fmt.Fprintf(page, "#### %s — %s\n\n",
			markdownCode(group.site), markdownText(location(group.members[0].Range)))

		for _, finding := range group.members {
			writeFindingLine(page, finding, baselineStatusLabel(finding))
		}

		page.WriteString("\n")
	}
}

func writeFindingLine(page *strings.Builder, finding Mutant, status string) {
	suffix := ""

	if finding.Verdict != nil && finding.Verdict.Diagnosis != "" {
		suffix = " — " + markdownText(string(finding.Verdict.Diagnosis))
	}

	if status != "" {
		suffix += " (" + markdownText(status) + ")"
	}

	fmt.Fprintf(page, "- %s %s at %s%s\n",
		markdownCode(finding.Operator), markdownText(string(finding.State)), markdownText(location(finding.Range)), suffix)
}

// baselineStatusLabel is the new-versus-accepted label, empty where no
// baseline judged the finding.
func baselineStatusLabel(finding Mutant) string {
	if finding.Provenance == nil {
		return ""
	}

	return finding.Provenance.BaselineStatus
}

// findingCountLabel keeps the overflow line's counts grammatical.
func findingCountLabel(count int) string {
	if count == 1 {
		return "1 finding"
	}

	return fmt.Sprintf("%d findings", count)
}

// M3d.1 (#51): the PR summary reporter. Its presentation contract is in
// docs/design/product-design.md, section 9.

// markdownFindingCap bounds the suggestion table a comment carries.
const markdownFindingCap = 10

// markdownSiteCap bounds the site groups the summary renders in full.
const markdownSiteCap = 25

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
		page.WriteString("> **Incomplete:** this score is untrustworthy — " +
			"a mutant timed out, nothing in the population could be scored, or a high share " +
			"of judged mutants were rejected by terraform validate.\n\n")
	}

	writePopulation(&page, value)
	writeGates(&page, value)
	writeFindingList(&page, value)
	writeSuggestionSummary(&page, value)
	writeWarningsSection(&page, value)

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
			condition = markdownTableCode(suggestion.Expression)
		}

		fmt.Fprintf(page, "| %s | %s | %s | %s |\n",
			markdownTableCode(suggestion.ID), markdownText(string(suggestion.Status)),
			markdownTableCode(suggestion.TargetFile+":"+suggestion.TargetRun), condition)
	}

	page.WriteString("\n")
}

// writeWarningsSection carries the report's warnings into the summary —
// preview's safety-gate verdict among them — where the terminal reporter
// puts its WARNINGS block: at the end, after the numbers.
func writeWarningsSection(page *strings.Builder, value Report) {
	if len(value.Warnings) == 0 {
		return
	}

	page.WriteString("### Warnings\n\n")

	for _, warning := range value.Warnings {
		fmt.Fprintf(page, "- %s\n", markdownText(warning))
	}

	page.WriteString("\n")
}

func writePopulation(page *strings.Builder, value Report) {
	fmt.Fprintf(page, "Population: %d selected, %d omitted, %d cached, %d fresh",
		value.Population.Selected, value.Population.Omitted,
		value.Population.Cached, value.Population.Fresh)

	if value.Selection.Mode == SelectionSince {
		fmt.Fprintf(page, " (%s", markdownCode("--since "+value.Selection.Ref))

		if value.Selection.ForcedFull != "" {
			fmt.Fprintf(page, "; full population forced: %s", markdownText(value.Selection.ForcedFull))
		}

		page.WriteString(")")
	}

	if value.Sampling != nil {
		fmt.Fprintf(page, " — sampled %.0f%% with seed %d, **not authoritative**",
			value.Sampling.RatePercent, value.Sampling.Seed)
	}

	page.WriteString("\n\n")
}

// writeGates renders the gate-table outcome. A table with no rows is noise,
// so a plain run that requested no gate and judged no baseline renders none.
func writeGates(page *strings.Builder, value Report) {
	if value.Gates == nil {
		return
	}

	populated := value.Gates.MinScore.Evaluated ||
		value.Gates.FailOnNew.Evaluated ||
		value.Gates.Baseline != nil
	if !populated {
		return
	}

	page.WriteString("| Gate | Outcome |\n| --- | --- |\n")

	if value.Gates.MinScore.Evaluated {
		fmt.Fprintf(page, "| `--min-score` | %s over the %s population%s |\n",
			passLabel(value.Gates.MinScore.Passed), markdownText(value.Gates.MinScore.Scope),
			markdownText(partialLabel(value.Gates.MinScore.Partial)))
	}

	if value.Gates.FailOnNew.Evaluated {
		fmt.Fprintf(page, "| `--fail-on-new` | %s over the %s population%s |\n",
			passLabel(value.Gates.FailOnNew.Passed), markdownText(value.Gates.FailOnNew.Scope),
			markdownText(partialLabel(value.Gates.FailOnNew.Partial)))
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
