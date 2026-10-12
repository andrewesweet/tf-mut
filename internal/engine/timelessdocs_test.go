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
// agent pages, the shipped skills — state their claims in the present tense, so
// the words that let a document rot are banned from them by test. Ticket T5 of
// the technical-documentation review seeds the list with the four temporal
// phrases; ticket T7's filler words join the same list when that ticket lands.
//
// One narrow exception, recorded here rather than taken quietly:
//
//   - docs/research and docs/reviews are dated records — exit-gate decisions
//     and review dispositions — whose temporal language is anchored to their
//     date, and whose quotations record the words under review; rewriting
//     either would falsify the record.
func TestTheDocumentsAvoidTheBannedPhrases(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	for _, name := range []string{"README.md", "CONTEXT.md", "ACTION.md"} {
		assertDocumentAvoidsTheBannedPhrases(t, filepath.Join(root, name))
	}
	for _, tree := range []string{
		"docs/adr", "docs/agents", "docs/design", "internal/skill/content",
	} {
		entries, err := os.ReadDir(filepath.Join(root, tree))
		if err != nil {
			t.Fatalf("reading %s: %v", tree, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			assertDocumentAvoidsTheBannedPhrases(t, filepath.Join(root, tree, entry.Name()))
		}
	}
}

// assertDocumentAvoidsTheBannedPhrases reports every banned hit outside a
// fenced code block, so one run names every line to restate.
func assertDocumentAvoidsTheBannedPhrases(t *testing.T, document string) {
	t.Helper()

	content, err := os.ReadFile(document) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", document, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, number := range bannedPhraseLines(lines) {
		t.Errorf("%s:%d names a banned temporal phrase: %q",
			document, number, strings.TrimSpace(lines[number-1]))
	}
}

func bannedPhraseLines(lines []string) []int {
	// Whole words only, so "concurrently" and "nowhere" pass; any case, so a
	// sentence-initial "Not yet" cannot hide.
	banned := regexp.MustCompile(`(?i)\b(currently|now|soon|not yet)\b`)

	var hits []int
	var paragraph []string
	var startLine int
	checkParagraph := func() {
		matches := banned.FindAllStringIndex(strings.Join(paragraph, " "), -1)
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

	inFence := false
	for number, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			checkParagraph()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		normalized := strings.Join(strings.Fields(line), " ")
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
		name    string
		content string
		lines   []int
	}{
		{name: "ordinary prose", content: "No licence is specified.\nConcurrently, nowhere.", lines: nil},
		{name: "all phrases", content: "Currently.\nNow.\nSoon.\nNot yet.", lines: []int{1, 2, 3, 4}},
		{name: "spaces", content: "It is not  yet supported.", lines: []int{1}},
		{name: "tabs", content: "It is not\t yet supported.", lines: []int{1}},
		{name: "unicode whitespace", content: "It is not\u00a0yet supported.", lines: []int{1}},
		{name: "wrapped", content: "A heading\n\nIt is not\nyet supported.", lines: []int{3}},
		{name: "CRLF wrapped", content: "A heading\r\n\r\nIt is not\r\nyet supported.", lines: []int{3}},
		{name: "multiple hits per line", content: "Currently, not\nyet available now or soon.\nNot  yet supported.", lines: []int{1, 2, 3}},
		{name: "paragraph boundary", content: "It is not\n \t\nyet supported.", lines: nil},
		{name: "fence boundary", content: "It is not\n```text\ncurrently now soon not yet\n```\nyet supported.", lines: nil},
		{name: "after fence", content: "```text\nnot yet\n```\nIt is not\nyet supported.", lines: []int{4}},
		{name: "before fence", content: "It is not\nyet supported.\n```text\nnot yet\n```", lines: []int{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			hits := bannedPhraseLines(strings.Split(test.content, "\n"))
			if !slices.Equal(hits, test.lines) {
				t.Fatalf("banned phrase source lines = %v, want %v", hits, test.lines)
			}
		})
	}
}
