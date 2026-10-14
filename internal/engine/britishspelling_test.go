package engine_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheDocumentsUseBritishSpelling holds the house style the
// technical-documentation review records: British English throughout, judged
// on consistency. It is seeded with the American forms the review names in
// the reader-facing set — analyzes, initialization and initializing in the
// README, modeling in the domain notes, recognized in the product design, and
// behavior and authorizes in the development skill — so the red run fails on
// the tree the review measured.
//
// The scan reads prose only: fenced code blocks and inline-code spans are
// identifiers, not dialect — the .artifacts path, flag names, and the
// /domain-modeling skill reference they name. That exemption is why the two
// "modeling" hits the review counts stay: both name the external skill, and
// renaming them would break the reference. Every other hit is prose and is
// fixed in British spelling.
func TestTheDocumentsUseBritishSpelling(t *testing.T) {
	t.Parallel()

	root, found := repositoryRoot(t)
	if !found {
		t.Fatal("repository root not found")
	}

	americanForms := `analyzes|initialization|initializing|modeling|recognized` +
		`|behavior|authorizes`
	american := regexp.MustCompile(`(?i)\b(` + americanForms + `)\b`)

	for _, name := range []string{
		"README.md",
		"docs/agents/domain.md",
		"docs/design/product-design.md",
		".claude/skills/tf-mut-development/SKILL.md",
	} {
		assertDocumentUsesBritishSpelling(t, filepath.Join(root, name), american)
	}
}

// assertDocumentUsesBritishSpelling reports every American form outside
// fenced code blocks and inline-code spans, so one run names every line to
// restate.
func assertDocumentUsesBritishSpelling(t *testing.T, document string, american *regexp.Regexp) {
	t.Helper()

	content, err := os.ReadFile(document) //nolint:gosec // a repository-owned path.
	if err != nil {
		t.Fatalf("reading %s: %v", document, err)
	}

	inFence := false
	for number, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, hit := range american.FindAllString(proseSegments(line), -1) {
			t.Errorf("%s:%d uses American spelling %q: %q",
				document, number+1, hit, strings.TrimSpace(line))
		}
	}
}

// proseSegments keeps only the text outside inline-code spans: the even
// segments of the backtick split. A trailing unclosed span counts as code,
// so an unbalanced backtick hides a line rather than flagging it.
func proseSegments(line string) string {
	segments := strings.Split(line, "`")
	prose := make([]string, 0, len(segments))
	for index, segment := range segments {
		if index%2 == 0 {
			prose = append(prose, segment)
		}
	}
	return strings.Join(prose, " ")
}
