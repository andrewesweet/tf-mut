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
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(declared) {
		t.Fatalf("VERSION declares %q, want a vX.Y.Z release version", declared)
	}

	uses := captureActionExampleToken(t, actionDoc, `uses: andrewesweet/tf-mut@(\S+)`)
	if uses != declared {
		t.Errorf("ACTION.md pins andrewesweet/tf-mut@%s, want the declared release version %s", uses, declared)
	}

	versionInput := captureActionExampleToken(t, actionDoc, `version:\s+(\S+)`)
	if versionInput != declared {
		t.Errorf("ACTION.md passes version: %s, want the declared release version %s", versionInput, declared)
	}

	// A floating major pin promises a tag the release process never cut and
	// the pipeline cannot verify; no such pin may return. A specific version
	// such as v1.0.0 is not a floating pin — it is held to the declared
	// version by the equality checks above.
	for _, floating := range []struct {
		name  string
		token *regexp.Regexp
	}{
		{"floating tf-mut uses ref", regexp.MustCompile(`tf-mut@v[0-9]+([^0-9.]|$)`)},
		{"floating tf-mut version input", regexp.MustCompile(`version:\s+v[0-9]+([^0-9.]|$)`)},
	} {
		if floating.token.MatchString(actionDoc) {
			t.Errorf("ACTION.md carries a %s; pin exact release versions", floating.name)
		}
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
