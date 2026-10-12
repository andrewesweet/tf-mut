package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestTheActionDocumentNamesTheDeclaredReleaseVersion is
// technical-documentation ticket T1 with release-it ticket T4: ACTION.md's
// install example must pin a version the release process declares, so a
// reader following the one documented install path cannot be sent to a tag
// that does not exist. The declared release version lives in the tracked
// one-line VERSION file the release workflow checks the pushed tag against;
// the coupling is deliberate — cutting a new release means moving VERSION
// and ACTION.md's example in the same change. The test deliberately never
// queries git tags: it must pass before the release it names is cut.
func TestTheActionDocumentNamesTheDeclaredReleaseVersion(t *testing.T) {
	t.Parallel()

	actionDoc := readDocument(t, "../../ACTION.md")
	declared := strings.TrimSpace(readDocument(t, "../../VERSION"))

	uses := captureActionExampleToken(t, actionDoc, `uses: andrewesweet/tf-mut@(\S+)`)
	if uses != declared {
		t.Errorf("ACTION.md pins andrewesweet/tf-mut@%s, want the declared release version %s", uses, declared)
	}

	versionInput := captureActionExampleToken(t, actionDoc, `version:\s+(\S+)`)
	if versionInput != declared {
		t.Errorf("ACTION.md passes version: %s, want the declared release version %s", versionInput, declared)
	}
}

// captureActionExampleToken extracts the first capture group of pattern from
// the document, failing the test rather than comparing against an absent
// token: the version coupling only holds when both sides are present.
func captureActionExampleToken(t *testing.T, document, pattern string) string {
	t.Helper()

	match := regexp.MustCompile(pattern).FindStringSubmatch(document)
	if len(match) < 2 {
		t.Fatalf("ACTION.md carries no %q token", pattern)
	}

	return match[1]
}
