package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The timeless-language contract, enforced rather than described: the documents
// a reader takes as current — README, CONTEXT, ACTION, the ADRs, the design and
// agent pages, the generated reference, the shipped skills — state their claims
// in the present tense and without filler, so the words that let a document rot
// or pad are banned from them by test. Ticket T5 of the
// technical-documentation review seeds the list with temporal phrases;
// ticket T7 adds filler words and positional references.
//
// Two narrow exceptions, recorded here rather than taken quietly:
//
//   - docs/research and docs/reviews are dated records — exit-gate decisions
//     and review dispositions — whose temporal language is anchored to their
//     date. Source quotations marked with <q> stay verbatim so the words under
//     review are preserved; authored blockquotes still follow the filler rule.
//   - Quoted diagnostics marked with <samp> stay verbatim in all documents.
func TestTheDocumentsAvoidTheBannedPhrases(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	for _, name := range []string{"README.md", "CONTEXT.md", "ACTION.md", "research/spikes/README.md"} {
		assertDocumentAvoidsTheBannedPhrases(t, filepath.Join(root, name), false)
	}
	for _, tree := range []string{
		"docs/adr", "docs/agents", "docs/design", "docs/reference", "docs/research", "docs/reviews", "internal/skill/content",
	} {
		entries, err := os.ReadDir(filepath.Join(root, tree))
		if err != nil {
			t.Fatalf("reading %s: %v", tree, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			assertDocumentAvoidsTheBannedPhrases(t, filepath.Join(root, tree, entry.Name()),
				tree == "docs/research" || tree == "docs/reviews")
		}
	}
}

// assertDocumentAvoidsTheBannedPhrases applies the language contract, so one
// run names every line to restate.
func assertDocumentAvoidsTheBannedPhrases(t *testing.T, document string, datedRecord bool) {
	t.Helper()

	content, err := os.ReadFile(document) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", document, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, number := range bannedPhraseLines(lines, datedRecord) {
		t.Errorf("%s:%d names a banned phrase: %q",
			document, number, strings.TrimSpace(lines[number-1]))
	}
}

func bannedPhraseLines(lines []string, datedRecord bool) []int {
	// Whole words only, so "concurrently" and "nowhere" pass; any case, so a
	// sentence-initial "Not yet" cannot hide.
	phrases := "actually|very|in order to|see above|see below"
	quotedPattern := `<samp>.*?</samp>`
	if !datedRecord {
		phrases += "|currently|now|soon|not yet"
	} else {
		quotedPattern += `|<q>.*?</q>`
	}
	banned := regexp.MustCompile(`(?i)\b(` + phrases + `)\b`)
	quoted := regexp.MustCompile(quotedPattern)

	var hits []int
	var paragraph []string
	var startLine int
	checkParagraph := func() {
		prose := strings.Join(paragraph, " ")
		for _, span := range quoted.FindAllStringIndex(prose, -1) {
			prose = prose[:span[0]] + strings.Repeat(" ", span[1]-span[0]) + prose[span[1]:]
		}
		matches := banned.FindAllStringIndex(prose, -1)
		offset := 0
		for index, line := range paragraph {
			end := offset + len(line)
			if len(matches) > 0 && matches[0][0] < end {
				hits = append(hits, startLine+index)
			}
			for len(matches) > 0 && matches[0][0] < end {
				matches = matches[1:]
			}
			offset = end + 1
		}
		paragraph = paragraph[:0]
	}

	var fence string
	for number, line := range lines {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), "> \t")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			checkParagraph()
			fence = trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, trimmed[:1]))]
			continue
		}
		normalized := strings.Join(strings.Fields(trimmed), " ")
		if normalized == "" {
			checkParagraph()
			continue
		}
		if len(paragraph) == 0 {
			startLine = number + 1
		}
		paragraph = append(paragraph, normalized)
	}
	checkParagraph()
	return hits
}

func TestTheTimelessLanguageGuardNormalizesParagraphWhitespace(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		content     string
		lines       []int
		datedRecord bool
	}{
		{name: "ordinary prose", content: "No licence is specified.\nConcurrently, nowhere.", lines: nil},
		{name: "all phrases", content: "Currently.\nNow.\nSoon.\nNot yet.", lines: []int{1, 2, 3, 4}},
		{name: "spaces", content: "It is not  yet supported.", lines: []int{1}},
		{name: "tabs", content: "It is not\t yet supported.", lines: []int{1}},
		{name: "unicode whitespace", content: "It is not\u00a0yet supported.", lines: []int{1}},
		{name: "wrapped", content: "A heading\n\nIt is not\nyet supported.", lines: []int{3}},
		{name: "CRLF wrapped", content: "A heading\r\n\r\nIt is not\r\nyet supported.", lines: []int{3}},
		{
			name:    "multiple hits per line",
			content: "Currently, not\nyet available now or soon.\nNot  yet supported.",
			lines:   []int{1, 2, 3},
		},
		{name: "paragraph boundary", content: "It is not\n \t\nyet supported.", lines: nil},
		{name: "fence boundary", content: "It is not\n```text\ncurrently now soon not yet\n```\nyet supported.", lines: nil},
		{name: "after fence", content: "```text\nnot yet\n```\nIt is not\nyet supported.", lines: []int{4}},
		{name: "before fence", content: "It is not\nyet supported.\n```text\nnot yet\n```", lines: []int{1}},
		{name: "tilde fence", content: "~~~text\nactually very in order to see above\n~~~\nSee below.", lines: []int{4}},
		{name: "long fence", content: "````text\n```\nvery useful\n````\nActually.", lines: []int{5}},
		{name: "filler words", content: "It actually generates very few mutants.\nSee above.", lines: []int{1, 2}},
		{name: "in order to", content: "What it inspects in order\nto kill it.", lines: []int{1}},
		{name: "quoted diagnostic", content: "It rejects (<samp>\"must refer to var.<name> in order to test incoming values\"</samp>).", lines: nil},
		{
			name:    "wrapped diagnostic",
			content: "It rejects (<samp>\"must refer to var.<name> in order\nto test incoming values\"</samp>).",
			lines:   nil,
		},
		{name: "CRLF diagnostic", content: "It rejects (<samp>\"in order\r\nto test\"</samp>).\r\nActually.", lines: []int{3}},
		{
			name:    "prose around wrapped diagnostic",
			content: "Actually, it rejects (<samp>\"must refer to var.<name> in order\nto test incoming values\"</samp>).\nThe check is very useful.",
			lines:   []int{1, 3},
		},
		{name: "ordinary quoted prose", content: "The suite is \"very useful\".", lines: []int{1}},
		{name: "wrapped quoted prose", content: "The suite is \"very\nuseful\".", lines: []int{1}},
		{name: "curly quoted prose", content: "The suite is “very useful”.", lines: []int{1}},
		{name: "unclosed diagnostic", content: "It rejects (<samp>\"in order to test\").", lines: []int{1}},
		{name: "diagnostic boundary", content: "It checks in <samp>\"in order to test\"</samp> order to fail.", lines: nil},
		{name: "current quotation", content: "The suite is <q>very useful</q>.", lines: []int{1}},
		{name: "current blockquote", content: "> The suite is very useful.", lines: []int{1}},
		{name: "dated temporal prose", content: "Currently. Now. Soon. Not yet.", datedRecord: true, lines: nil},
		{name: "dated filler prose", content: "Actually, very useful in order to test.\nSee above. See below.", datedRecord: true, lines: []int{1, 2}},
		{name: "dated wrapped filler", content: "It checks in order\nto fail.", datedRecord: true, lines: []int{1}},
		{name: "dated quoted prose", content: "The suite is \"very useful\".", datedRecord: true, lines: []int{1}},
		{name: "dated source quotation", content: "The review states <q>actually very\nuseful in order to test</q>.", datedRecord: true, lines: nil},
		{name: "dated authored blockquote", content: "> Actually, very useful in order to test.", datedRecord: true, lines: []int{1}},
		{
			name:        "dated measurement callout",
			content:     "A heading\n\n> Everything actually measured here was executed against `Terraform v1.15.8 on linux_amd64` with the\n> engine at the commit that introduced this file.",
			datedRecord: true,
			lines:       []int{3},
		},
		{
			name:    "wrapped callout phrases",
			content: "> It checks in order\n> to fail.\n>\n> See\n> above.\n>\n> See\n> below.",
			lines:   []int{1, 4, 7},
		},
		{
			name:        "dated wrapped callout phrases",
			content:     "> It checks in order\n> to fail.\n>\n> See\n> above.\n>\n> See\n> below.",
			datedRecord: true,
			lines:       []int{1, 4, 7},
		},
		{name: "nested callout", content: "\t> > It checks in order\n\t> > to fail.", lines: []int{1}},
		{name: "dated nested callout", content: "\t> > It checks in order\n\t> > to fail.", datedRecord: true, lines: []int{1}},
		{name: "callout paragraph boundary", content: "> It checks in\n>\n> order to fail.", lines: nil},
		{name: "dated temporal callout", content: "> Currently. Now. Soon. Not yet.", datedRecord: true, lines: nil},
		{
			name:        "marked callout quotation",
			content:     "> <q>Actually, very useful in order\n> to test.</q>\n> See below.",
			datedRecord: true,
			lines:       []int{3},
		},
		{name: "callout diagnostic", content: "> <samp>\"in order\n> to test\"</samp>.\n> Actually.", lines: []int{3}},
		{name: "callout fence", content: "> ~~~text\n> actually very in order to see above\n> ~~~\n> See below.", lines: []int{4}},
		{name: "dated callout fence", content: "> ```text\n> actually very in order to see above\n> ```\n> See below.", datedRecord: true, lines: []int{4}},
		{name: "dated diagnostic", content: "It rejects (<samp>\"in order\nto test\"</samp>).", datedRecord: true, lines: nil},
		{
			name:        "prose around dated quotation",
			content:     "Actually, the review states <q>very\nuseful</q>.\nSee below.",
			datedRecord: true,
			lines:       []int{1, 3},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			hits := bannedPhraseLines(strings.Split(test.content, "\n"), test.datedRecord)
			if !slices.Equal(hits, test.lines) {
				t.Fatalf("banned phrase source lines = %v, want %v", hits, test.lines)
			}
		})
	}
}
