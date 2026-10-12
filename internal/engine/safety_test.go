package engine_test

import (
	"errors"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

func TestUnmockedProviderIsRefusedWithoutTheFlag(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unmocked")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(err, engine.ErrRealInfrastructure) {
		t.Fatalf("error = %v, want a real-infrastructure refusal", err)
	}

	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("refusal does not name the unmocked provider: %v", err)
	}

	if !strings.Contains(err.Error(), "--allow-real-infrastructure") {
		t.Fatalf("refusal does not name the opt-in flag: %v", err)
	}
}

func TestUnmockedProviderRunsWithTheFlag(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	module := copyFixture(t, "unmocked")

	config := baseConfig(t, module)
	config.AllowRealInfrastructure = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}

	if len(result.Warnings) == 0 {
		t.Fatal("running against an unmocked provider must warn")
	}
}

func TestProvisionerIsRefusedAndNeverExecuted(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	_, err := engine.Run(t.Context(), config)
	if !errors.Is(err, engine.ErrUnsandboxedEffects) {
		t.Fatalf("error = %v, want an unsandboxed-effects refusal", err)
	}

	if !strings.Contains(err.Error(), "provisioner") {
		t.Fatalf("refusal does not name the offending construct: %v", err)
	}

	if !strings.Contains(err.Error(), "main.tf:") {
		t.Fatalf("refusal does not carry a source range: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the provisioner executed despite the refusal")
	}
}

func TestProvisionerRunsOnlyWithTheSecondOptIn(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.AllowUnsandboxedEffects = true
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the opt-in did not let the apply-mode run execute: %v", statErr)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}
}

func TestPlanOnlySuiteWithEffectsStillProceeds(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	rewritePlanMode(t, filepath.Join(module, "tests", "unit.tftest.hcl"))

	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := baseConfig(t, module)
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("a plan-mode suite must not be refused: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated")
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a plan-mode run executed the provisioner")
	}
}

// rewritePlanMode converts the fixture's apply-mode run to plan mode, which is
// the severable remainder the effects gate must let through.
func rewritePlanMode(t *testing.T, path string) {
	t.Helper()

	content, err := os.ReadFile(path) //nolint:gosec // the path is a test-owned fixture copy.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	rewritten := strings.Replace(string(content), "command = apply", "command = plan", 1)

	//nolint:gosec // the path is a test-owned fixture copy.
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestSourceTreeIsUntouchedByEveryRun(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"skeleton", discriminateFixture, "count-tolerant", "foreach"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, fixture)
			before := treeDigest(t, module)

			if _, err := engine.Run(t.Context(), baseConfig(t, module)); err != nil {
				t.Fatalf("run: %v", err)
			}

			assertTreeUnchanged(t, module, before)
		})
	}
}

func TestKilledMutantsNeverPayForValidation(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "skeleton")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range result.Mutants {
		if mutant.State != report.Killed && mutant.State != report.Survived {
			continue
		}

		if mutant.Validated {
			t.Fatalf("mutant %s (%s) paid for terraform validate", mutant.ID, mutant.State)
		}
	}
}

func TestUnmockedProviderRefusalNamesTheOffendingBlock(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "unmocked")

	_, err := engine.Run(t.Context(), baseConfig(t, module))
	if err == nil {
		t.Fatal("expected a refusal")
	}

	if !strings.Contains(err.Error(), "null_resource.app") {
		t.Fatalf("refusal does not name the offending block: %v", err)
	}

	if !strings.Contains(err.Error(), "main.tf:") {
		t.Fatalf("refusal does not carry a source range: %v", err)
	}
}

func TestPreviewIsNeverRefusedBecauseItExecutesNothing(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")
	marker := filepath.Join(t.TempDir(), "provisioner-ran")

	config := previewRequest(t, module)
	config.Env = append(config.Env, "TF_MUT_MARKER="+marker)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("preview must not be refused: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("preview generated no mutants")
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("preview executed the provisioner")
	}
}

// reporterMarkdown names the markdown reporter in the render tables of the
// engine's reporter tests.
const reporterMarkdown = "markdown"

// TestPreviewReportsWhatTheSafetyGatesWouldDecide: preview must surface the
// safety gates' verdict — the one output a preview has — through the
// discarding reporters too, so every consumer of a report sees the gates'
// decision.
func TestPreviewReportsWhatTheSafetyGatesWouldDecide(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")

	result, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// The gates would refuse the grading run: the preview does not refuse,
	// but it must carry their verdict, so the person reading the population
	// sees the risk the run would have stopped at.
	if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
		return strings.Contains(warning, "the safety gates would refuse a mutation run")
	}) {
		t.Fatalf("a gated module's preview carried no gate verdict: %v", result.Warnings)
	}

	verdict := ""

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "the safety gates would refuse a mutation run") {
			verdict = warning

			break
		}
	}

	if !strings.Contains(verdict, "provisioner") {
		t.Fatalf("the gate verdict does not name the offending construct: %s", verdict)
	}

	// The warning names the same refusal the run produces, quoted: the
	_, runErr := engine.Run(t.Context(), baseConfig(t, module))
	if !errors.Is(runErr, engine.ErrUnsandboxedEffects) {
		t.Fatalf("run error = %v, want the unsandboxed-effects refusal", runErr)
	}

	if !strings.Contains(verdict, runErr.Error()) {
		t.Fatalf("the preview verdict %q does not quote the run refusal %q", verdict, runErr.Error())
	}
}

// TestPreviewSafetyVerdictReachesTheDiscardingReporters: the preview's
// recorded safety-gate verdict survives every reporter. The markdown, HTML,
// JUnit and MTE renderings used to discard the report's warnings, so
// `preview --reporter <that>` on a gated module printed no safety outcome
// at all — exactly the artefact a pipeline reads.
func TestPreviewSafetyVerdictReachesTheDiscardingReporters(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "provisioner")

	result, err := engine.Run(t.Context(), previewRequest(t, module))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	verdict := ""

	for _, warning := range result.Warnings {
		if strings.Contains(warning, "the safety gates would refuse a mutation run") {
			verdict = warning

			break
		}
	}

	if verdict == "" {
		t.Fatalf("the preview carried no gate verdict: %v", result.Warnings)
	}

	for name, write := range map[string]func(io.Writer, report.Report) error{
		reporterMarkdown: report.WriteMarkdown,
		"html":           report.WriteHTML,
		"junit":          report.WriteJUnit,
		"mte":            report.WriteMTE,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rendered := strings.Builder{}
			if err := write(&rendered, result); err != nil {
				t.Fatalf("rendering: %v", err)
			}

			// Every line of the verdict must survive: XML and JSON escaping
			// rewrite newlines (&#xA;, \n), so the assertion is per line.
			for line := range strings.SplitSeq(verdict, "\n") {
				line = strings.TrimSpace(line)
				page := rendered.String()
				if name == reporterMarkdown {
					page = markdownTextContent(page)
				}

				if line != "" && !strings.Contains(page, line) {
					t.Fatalf("the %s reporter dropped the preview's safety-gate verdict line %q:\n%s",
						name, line, rendered.String())
				}
			}
		})
	}
}

func markdownTextContent(value string) string {
	return html.UnescapeString(regexp.MustCompile(`\\([[:punct:]])`).ReplaceAllString(value, "$1"))
}

func TestPreviewMarkdownEscapesSafetyWarningLocations(t *testing.T) {
	t.Parallel()

	for _, entry := range []struct {
		file    string
		escaped string
	}{
		{"main<env>.tf", "main&lt;env&gt;\\.tf"},
		{"main*env*.tf", "main\\*env\\*\\.tf"},
	} {
		t.Run(entry.file, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "provisioner")
			if err := os.Rename(filepath.Join(module, "main.tf"), filepath.Join(module, entry.file)); err != nil {
				t.Fatalf("renaming fixture source: %v", err)
			}

			result, err := engine.Run(t.Context(), previewRequest(t, module))
			if err != nil {
				t.Fatalf("preview: %v", err)
			}

			if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
				return strings.Contains(warning, entry.file)
			}) {
				t.Fatalf("the safety warning lost the source filename: %v", result.Warnings)
			}

			rendered := strings.Builder{}
			if err := report.WriteMarkdown(&rendered, result); err != nil {
				t.Fatalf("rendering: %v", err)
			}

			assertMarkdownSafetyWarnings(t, rendered.String(), result.Warnings)

			if !strings.Contains(rendered.String(), entry.escaped) || strings.Contains(rendered.String(), entry.file) {
				t.Fatalf("the preview renders a filename as markup:\n%s", rendered.String())
			}
		})
	}
}

func assertMarkdownSafetyWarnings(t *testing.T, page string, warnings []string) {
	t.Helper()

	text := markdownTextContent(page)

	for _, warning := range warnings {
		if !strings.Contains(text, warning) {
			t.Fatalf("the markdown preview lost the literal safety warning %q:\n%s", warning, page)
		}
	}
}

func TestResourcesWithoutAnExtremeMutantAreReported(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "no-optional-arguments")

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	joined := strings.Join(result.Warnings, " ")
	if !strings.Contains(joined, "terraform_data.bare") {
		t.Fatalf("a resource no extreme mutant could reach was not reported: %v", result.Warnings)
	}

	for _, finding := range result.Findings {
		if finding.Address == "terraform_data.bare" {
			t.Fatal("a resource with no executed extreme mutant must not be called pseudo-tested")
		}
	}
}
