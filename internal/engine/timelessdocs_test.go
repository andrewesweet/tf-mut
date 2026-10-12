package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
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

	// Whole words only, so "concurrently" and "nowhere" pass; any case, so a
	// sentence-initial "Not yet" cannot hide.
	banned := regexp.MustCompile(`(?i)\b(currently|now|soon|not yet)\b`)

	inFence := false
	for number, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if banned.MatchString(line) {
			t.Errorf("%s:%d names a banned temporal phrase: %q",
				document, number+1, strings.TrimSpace(line))
		}
	}
}
