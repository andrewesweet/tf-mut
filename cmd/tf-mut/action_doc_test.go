package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var (
	errActionDocumentMissingExample  = errors.New("ACTION.md has no YAML example")
	errActionDocumentMissingFence    = errors.New("ACTION.md has no closing YAML fence")
	errActionDocumentMissingStep     = errors.New("ACTION.md's YAML example has no tf-mut step")
	errActionDocumentVersionMismatch = errors.New("ACTION.md release version mismatch")
)

type actionDocumentJob struct {
	Steps []actionDocumentStep `yaml:"steps"`
}

type actionDocumentStep struct {
	Uses string               `yaml:"uses"`
	With actionDocumentInputs `yaml:"with"`
}

type actionDocumentInputs struct {
	Version string `yaml:"version"`
}

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

	if err := checkActionDocumentVersion(actionDoc, declared); err != nil {
		t.Fatal(err)
	}
}

func checkActionDocumentVersion(document, declared string) error {
	_, example, found := strings.Cut(document, "```yaml\n")
	if !found {
		return errActionDocumentMissingExample
	}
	example, _, found = strings.Cut(example, "\n```")
	if !found {
		return errActionDocumentMissingFence
	}

	var workflow struct {
		Jobs map[string]actionDocumentJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(example), &workflow); err != nil {
		return fmt.Errorf("parse ACTION.md's YAML example: %w", err)
	}

	found = false
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			action, ref, _ := strings.Cut(step.Uses, "@")
			if action != "andrewesweet/tf-mut" {
				continue
			}
			found = true
			if ref != declared || step.With.Version != declared {
				return fmt.Errorf("%w: pins %q with version %q, want declared release %q",
					errActionDocumentVersionMismatch, step.Uses, step.With.Version, declared)
			}
		}
	}
	if !found {
		return errActionDocumentMissingStep
	}

	return nil
}

func TestActionDocumentVersionChecksActiveYAMLFields(t *testing.T) {
	t.Parallel()

	const releaseVersion = "v0.1.0"

	for _, test := range []struct {
		name     string
		steps    string
		declared string
		wantErr  bool
	}{
		{
			name: "quoted pins",
			steps: `      - uses: "andrewesweet/tf-mut@v0.1.0"
        with:
          version: 'v0.1.0'`,
			declared: releaseVersion,
		},
		{
			name: "commented pins cannot hide a stale uses ref",
			steps: `      # uses: andrewesweet/tf-mut@v0.1.0
      # version: v0.1.0
      - uses: andrewesweet/tf-mut@v0.0.1
        with:
          version: v0.1.0`,
			declared: releaseVersion,
			wantErr:  true,
		},
		{
			name: "commented pins cannot hide a stale version input",
			steps: `      # version: v0.1.0
      - uses: andrewesweet/tf-mut@v0.1.0
        with:
          version: v0.0.1`,
			declared: releaseVersion,
			wantErr:  true,
		},
		{
			name: "a Terraform version belongs to its own step",
			steps: `      - uses: hashicorp/setup-terraform@v4
        with:
          terraform_version: 1.15.8
      - uses: andrewesweet/tf-mut@v0.1.0
        with:
          version: v0.1.0`,
			declared: releaseVersion,
		},
		{
			name: "equal prerelease pins",
			steps: `      - uses: andrewesweet/tf-mut@v0.1.0-rc.1
        with:
          version: v0.1.0-rc.1`,
			declared: "v0.1.0-rc.1",
		},
		{
			name: "every tf-mut step must match",
			steps: `      - uses: andrewesweet/tf-mut@v0.1.0
        with:
          version: v0.1.0
      - uses: andrewesweet/tf-mut@v0.0.1
        with:
          version: v0.0.1`,
			declared: releaseVersion,
			wantErr:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			document := "Example: tf-mut@v1, version: v1\n\n```yaml\njobs:\n  mutate:\n    steps:\n" +
				test.steps + "\n```\n"
			err := checkActionDocumentVersion(document, test.declared)
			if (err != nil) != test.wantErr {
				t.Fatalf("version check error = %v, want error %v", err, test.wantErr)
			}
		})
	}
}
