package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
)

// M3d.1 (#51): the HTML reporter is a self-contained single file — no network
// fetch, no external assets. The mutation-testing-elements viewer is embedded
// at the pinned version with its licence shipped alongside, and tf-mut's
// authoritative metrics render above the viewer, so the page never presents
// the recomputed score as the headline.

//go:embed assets/mutation-test-elements.js
var mteBundle string

//go:embed assets/mutation-testing-elements-LICENSE
var mteLicence string

// WriteHTML renders the self-contained report page.
func WriteHTML(writer io.Writer, value Report) error {
	document := mteDocument(value)

	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encoding embedded report: %w", err)
	}

	page := strings.Builder{}
	page.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	page.WriteString("<title>tf-mut report</title>\n")
	fmt.Fprintf(&page, "<!-- mutation-testing-elements v%s, embedded. Licence:\n\n%s\n-->\n",
		MTEVersion, escapeComment(mteLicence))
	page.WriteString("<script>")
	page.WriteString(escapeScript(mteBundle))
	page.WriteString("</script>\n</head>\n<body>\n")

	writeAuthoritativeHeader(&page, value)

	page.WriteString("<mutation-test-report-app title-text=\"tf-mut\"></mutation-test-report-app>\n")
	page.WriteString("<script>document.querySelector('mutation-test-report-app').report = ")
	page.WriteString(escapeScript(string(encoded)))
	page.WriteString(";</script>\n</body>\n</html>\n")

	if _, err := io.WriteString(writer, page.String()); err != nil {
		return fmt.Errorf("writing HTML report: %w", err)
	}

	return nil
}

// writeAuthoritativeHeader renders tf-mut's own metrics above the viewer:
// the viewer's recomputed score is the lossy one, and the page says so.
func writeAuthoritativeHeader(page *strings.Builder, value Report) {
	page.WriteString("<section style=\"font-family: system-ui, sans-serif; margin: 1em;\">\n")
	page.WriteString("<h1>tf-mut — authoritative metrics</h1>\n")
	fmt.Fprintf(page,
		"<p><strong>Mutation score %.1f%%</strong> &middot; assertion score %.1f%% &middot; "+
			"reachability %.1f%% &middot; scored %d</p>\n",
		value.Metrics.MutationScore*percent,
		value.Metrics.AssertionScore*percent,
		value.Metrics.Reachability*percent,
		value.Metrics.Scored)

	if len(value.Warnings) > 0 {
		page.WriteString("<p><strong>Warnings</strong></p>\n<ul>\n")

		for _, warning := range value.Warnings {
			fmt.Fprintf(page, "<li>%s</li>\n", html.EscapeString(warning))
		}

		page.WriteString("</ul>\n")
	}

	writeIndeterminateCauses(page, value)

	if value.Metrics.Incomplete {
		page.WriteString("<p><strong>Incomplete:</strong> this score is untrustworthy &mdash; " +
			"an operational-failure trip stopped execution, a mutant timed out, " +
			"a mutant hit an execution error, " +
			"nothing in the population could be scored, or a high share " +
			"of judged mutants were rejected by terraform validate.</p>\n")
	}

	page.WriteString("<p>The viewer below recomputes a score from mapped statuses and " +
		"<em>will disagree</em>: the mapping is a declared-lossy interoperability adapter. " +
		"These numbers are the authoritative ones.</p>\n</section>\n")
}

// writeIndeterminateCauses renders the survivors diagnosed
// indeterminate-unknown-values collapsed by their shared unknown paths, as
// the terminal and markdown reporters do: the sentence 'N survivors are
// indeterminate because these paths are unknown at plan' said once per
// distinct path set with the paths and the shared fix, groups in their first
// appearance in report order. The viewer below keeps each mutant's own
// record; this is where the shared cause is stated.
func writeIndeterminateCauses(page *strings.Builder, value Report) {
	groups, _ := partitionIndeterminate(value.Survivors())
	if len(groups) == 0 {
		return
	}

	page.WriteString("<p><strong>Indeterminate at plan</strong></p>\n<ul>\n")

	for _, group := range groups {
		count, verb := fmt.Sprintf("%d survivors", len(group.members)), "are"
		if len(group.members) == 1 {
			count, verb = "1 survivor", "is"
		}

		fmt.Fprintf(page, "<li>%s %s indeterminate because these paths are unknown at plan:\n<ul>\n", count, verb)

		for _, path := range group.paths {
			fmt.Fprintf(page, "<li><code>%s</code></li>\n", html.EscapeString(path))
		}

		page.WriteString("</ul>\n")

		if verdict := group.members[0].Verdict; verdict != nil && verdict.Fix != "" {
			fmt.Fprintf(page, "Fix: %s\n", html.EscapeString(verdict.Fix))
		}

		page.WriteString("</li>\n")
	}

	page.WriteString("</ul>\n")
}

// escapeScript keeps inline content from terminating its script element.
func escapeScript(content string) string {
	return strings.ReplaceAll(content, "</", "<\\/")
}

// escapeComment keeps the licence from terminating its comment.
func escapeComment(content string) string {
	return strings.ReplaceAll(content, "--", "- -")
}
