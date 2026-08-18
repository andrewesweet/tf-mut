package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// M4a: generation behind the three fail-closed adapters, through the engine
// seam. The adapter matrices themselves live in `internal/suggest`, over the
// payload paths and provider types they are contracts about; what is asserted
// here is that each of their outcomes reaches the report where the matrix says
// it must, and that no artefact of a skipped suggestion says more than it may.

const (
	suggestBasicFixture     = "suggest-basic"
	suggestSensitiveFixture = "suggest-sensitive"
	suggestJSONFixture      = "suggest-json-target"
	suggestMultiFixture     = "suggest-multifile"
	secretValue             = "s3cret-value"
	unitTestFile            = "tests/unit.tftest.hcl"
)

// suggestConfig is a suggest run that verifies.
func suggestConfig(t *testing.T, module string) engine.Config {
	t.Helper()

	config := baseConfig(t, module)
	config.Suggest = true

	return config
}

// dryRunConfig is a suggest run that generates and verifies nothing.
func dryRunConfig(t *testing.T, module string) engine.Config {
	t.Helper()

	config := suggestConfig(t, module)
	config.SuggestDryRun = true

	return config
}

func runSuggest(t *testing.T, config engine.Config) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}

	return result
}

func TestSuggestGeneratesTheAssertionThatWouldHaveKilledASurvivor(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunConfig(t, copyFixture(t, suggestBasicFixture)))

	candidates := withStatus(result, report.SuggestionCandidate)
	if len(candidates) == 0 {
		t.Fatalf("no candidate was generated; statuses were %s", suggest.Statuses(result.Suggestions))
	}

	for _, candidate := range candidates {
		if candidate.Expression != `output.ignored == "nobody-checks-me"` {
			t.Fatalf("expression = %q, want the typed scalar equality", candidate.Expression)
		}

		if !strings.Contains(candidate.Patch, "assert {") {
			t.Fatalf("the candidate carries no patch that adds an assertion: %q", candidate.Patch)
		}

		if candidate.TargetFile != unitTestFile || candidate.TargetRun != "applied" {
			t.Fatalf("placement = %s:%s, want the run that carried the delta",
				candidate.TargetFile, candidate.TargetRun)
		}
	}

	if result.Command != report.CommandSuggest {
		t.Fatalf("command = %s, want suggest", result.Command)
	}
}

func TestADryRunVerifiesNothing(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunConfig(t, copyFixture(t, suggestBasicFixture)))

	for _, suggestion := range result.Suggestions {
		if suggestion.Verification != nil {
			t.Fatalf("suggestion %s carries verification evidence from a dry run", suggestion.ID)
		}

		if suggestion.Status == report.SuggestionVerified ||
			suggestion.Status == report.SuggestionRefuted {
			t.Fatalf("suggestion %s reached %s without being verified", suggestion.ID, suggestion.Status)
		}
	}
}

func TestSuggestionIdentifiersAreStableAcrossRunsAndUnrelatedEdits(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, suggestBasicFixture)

	first := identifiersOf(runSuggest(t, dryRunConfig(t, module)))
	second := identifiersOf(runSuggest(t, dryRunConfig(t, module)))

	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("identifiers changed across runs:\n%v\n%v", first, second)
	}

	writeFile(t, filepath.Join(module, "unrelated.tf"),
		"resource \"terraform_data\" \"unrelated\" {\n  input = \"elsewhere\"\n}\n")

	third := identifiersOf(runSuggest(t, dryRunConfig(t, module)))
	for _, id := range first {
		if !slices.Contains(third, id) {
			t.Fatalf("identifier %s did not survive an unrelated edit: %v", id, third)
		}
	}
}

func TestIndeterminateSurvivorsAndUnassertableMutantsReceiveNoSuggestion(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"contract", "precedence-unknown"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			result := runSuggest(t, dryRunConfig(t, copyFixture(t, fixture)))

			suggested := map[string]bool{}
			for _, suggestion := range result.Suggestions {
				suggested[suggestion.MutantID] = true
			}

			for _, mutant := range result.Mutants {
				if !suggested[mutant.ID] {
					continue
				}

				if mutant.State != report.Survived {
					t.Fatalf("mutant %s is %s and still received a suggestion", mutant.ID, mutant.State)
				}

				if !mutant.Verdict.Diagnosis.Actionable() {
					t.Fatalf("mutant %s is %s and still received a suggestion",
						mutant.ID, mutant.Verdict.Diagnosis)
				}
			}
		})
	}
}

func TestAJSONTestTargetIsSkippedWithNoPatch(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunConfig(t, copyFixture(t, suggestJSONFixture)))

	skipped := withStatus(result, report.SuggestionSkippedUnsupportedTarget)
	if len(skipped) == 0 {
		t.Fatalf("a JSON-declared run produced no unsupported-target skip: %s",
			suggest.Statuses(result.Suggestions))
	}

	for _, suggestion := range skipped {
		if suggestion.Patch != "" || suggestion.Expression != "" {
			t.Fatalf("suggestion %s carries an artefact for a target nothing can write", suggestion.ID)
		}

		if !strings.HasSuffix(suggestion.TargetFile, ".tftest.json") {
			t.Fatalf("target = %s, want the JSON test file", suggestion.TargetFile)
		}
	}
}

// TestASensitiveValueReachesNoSuggestionArtefact is the M3 disposition's rule:
// no expression, no patch, no status reason, and nothing any reporter renders.
func TestASensitiveValueReachesNoSuggestionArtefact(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunConfig(t, copyFixture(t, suggestSensitiveFixture)))

	for _, suggestion := range result.Suggestions {
		for name, artefact := range map[string]string{
			"expression": suggestion.Expression,
			"patch":      suggestion.Patch,
			"reason":     suggestion.StatusReason,
		} {
			if strings.Contains(artefact, secretValue) {
				t.Fatalf("suggestion %s leaks the sensitive value through its %s", suggestion.ID, name)
			}
		}
	}

	for name, rendered := range renderAll(t, result) {
		if strings.Contains(rendered, secretValue) {
			t.Fatalf("the %s reporter leaks the sensitive value", name)
		}
	}
}

// TestEverySkippedStatusCarriesNoPatchAndAReason is the outcome table's
// presence rules, over whatever mix of skips the corpus produces.
func TestEverySkippedStatusCarriesNoPatchAndAReason(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{
		suggestBasicFixture, suggestSensitiveFixture, suggestJSONFixture, suggestMultiFixture,
	} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			result := runSuggest(t, dryRunConfig(t, copyFixture(t, fixture)))

			for _, suggestion := range result.Suggestions {
				assertPresenceRules(t, suggestion)
			}
		})
	}
}

func assertPresenceRules(t *testing.T, suggestion report.Suggestion) {
	t.Helper()

	if suggestion.ID == "" || suggestion.MutantID == "" ||
		suggestion.TargetFile == "" || suggestion.TargetRun == "" {
		t.Fatalf("suggestion %+v is missing one of the always-present fields", suggestion)
	}

	if !suggestion.Status.Skipped() {
		return
	}

	if suggestion.Patch != "" {
		t.Fatalf("suggestion %s is %s and still carries a patch", suggestion.ID, suggestion.Status)
	}

	if suggestion.StatusReason == "" {
		t.Fatalf("suggestion %s is %s and carries no reason", suggestion.ID, suggestion.Status)
	}

	if suggestion.VerifiedDigest != "" {
		t.Fatalf("suggestion %s is %s and still carries a verified digest", suggestion.ID, suggestion.Status)
	}
}

func withStatus(result report.Report, status report.SuggestionStatus) []report.Suggestion {
	matching := []report.Suggestion{}

	for _, suggestion := range result.Suggestions {
		if suggestion.Status == status {
			matching = append(matching, suggestion)
		}
	}

	return matching
}

func identifiersOf(result report.Report) []string {
	ids := make([]string, 0, len(result.Suggestions))
	for _, suggestion := range result.Suggestions {
		ids = append(ids, suggestion.ID)
	}

	return ids
}

// renderAll renders the report through every reporter a suggestion can reach,
// which is what "no artefact" has to mean to be worth anything.
func renderAll(t *testing.T, result report.Report) map[string]string {
	t.Helper()

	rendered := map[string]string{}

	for name, write := range map[string]func(*strings.Builder, report.Report) error{
		"terminal": func(builder *strings.Builder, value report.Report) error {
			return report.WriteTerminal(builder, value)
		},
		"json": func(builder *strings.Builder, value report.Report) error {
			return report.WriteJSON(builder, value)
		},
		"markdown": func(builder *strings.Builder, value report.Report) error {
			return report.WriteMarkdown(builder, value)
		},
	} {
		builder := strings.Builder{}
		if err := write(&builder, result); err != nil {
			t.Fatalf("rendering %s: %v", name, err)
		}

		rendered[name] = builder.String()
	}

	return rendered
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	return info
}

func requireNonRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory would not refuse a write")
	}
}

// TestAMutantSurfacedSecretReachesNoReport is the round-3 review's critical
// finding: a collection mutant moves a sensitive value into a path whose
// baseline value was public, so only the mutant payload carries the mark. The
// sensitivity predicate is decided over both payloads and over a secret set
// unioned across runs and sides, and every reporter must be value-free.
func TestAMutantSurfacedSecretReachesNoReport(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "sensitive-shift")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for name, rendered := range renderAll(t, result) {
		if strings.Contains(rendered, secretValue) {
			t.Fatalf("the %s reporter leaks the mutant-surfaced secret", name)
		}
	}
}

// TestARealSuggestReportValidatesAgainstThePublishedSchema closes the round-3
// review's contract gap: the published schema is validated against engine
// output, not only against a hand-built sample — including the outcome
// table's presence rules, which the schema now encodes.
func TestARealSuggestReportValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()

	schema := loadPublishedSchema(t)

	for name, config := range map[string]engine.Config{
		"verified": suggestConfig(t, copyFixture(t, suggestBasicFixture)),
		"skips":    dryRunConfig(t, copyFixture(t, suggestSensitiveFixture)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := runSuggest(t, config)
			if len(result.Suggestions) == 0 {
				t.Fatal("the fixture produced no suggestions to validate")
			}

			builder := strings.Builder{}
			if err := report.WriteJSON(&builder, result); err != nil {
				t.Fatalf("rendering: %v", err)
			}

			document := any(nil)
			if err := json.Unmarshal([]byte(builder.String()), &document); err != nil {
				t.Fatalf("decoding: %v", err)
			}

			if problems := validateAgainst(schema, schema, document, "$"); len(problems) > 0 {
				t.Fatalf("the real report does not validate:\n  %s", strings.Join(problems, "\n  "))
			}
		})
	}
}

func loadPublishedSchema(t *testing.T) map[string]any {
	t.Helper()

	content, err := os.ReadFile("../../docs/schema/report-2.2.0.json")
	if err != nil {
		t.Fatalf("reading the published schema: %v", err)
	}

	schema := map[string]any{}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decoding the published schema: %v", err)
	}

	return schema
}
