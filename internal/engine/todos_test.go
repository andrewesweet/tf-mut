package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M4.5b: input synthesis and the three TODO surfaces. The protocol's whole
// point is that "fails loudly", "green before write" and "edit to resume" all
// hold at once, which they only can if TODO material is a non-executable
// artefact class rather than a placeholder inside a test file.

const (
	untestedTodoFixture   = "untested-todo"
	untestedSecretFixture = "untested-secret-diagnostic"

	// answeredCIDR is the value the mandatory end-to-end case answers with.
	answeredCIDR = `"10.0.0.0/16"`
)

// TestAnUnsynthesizableInputBecomesANonExecutableArtefact is the first step of
// the mandatory end-to-end case.
func TestAnUnsynthesizableInputBecomesANonExecutableArtefact(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedTodoFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	block := result.Characterisation
	if block.OpenTodos() != 1 {
		t.Fatalf("open todos = %d, want one for the unsynthesizable input", block.OpenTodos())
	}

	if block.Complete {
		t.Fatal("a characterisation with an open judgement point reported complete")
	}

	if len(block.Files) != 1 || block.Files[0].Executable {
		t.Fatalf("the generated file is executable: %+v", block.Files)
	}

	if !strings.HasSuffix(block.Files[0].Path, ".tfmut-todo.hcl") {
		t.Fatalf("the artefact is not in the non-executable class: %s", block.Files[0].Path)
	}

	// Nothing `terraform test` reads was written, so the suite on disk is
	// still green by construction: the mutation loop refuses for want of run
	// blocks rather than failing on unverified content.
	entries, err := os.ReadDir(filepath.Join(module, "tests"))
	if err != nil {
		t.Fatalf("reading the test directory: %v", err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tftest.hcl") {
			t.Fatalf("an unverified test file was written: %s", entry.Name())
		}
	}

	if result.ExitCode(report.Gate{}) != report.ExitFindings { //nolint:exhaustruct // no gate is requested.
		t.Fatalf("exit code = %d, want one while a judgement point is open",
			result.ExitCode(report.Gate{})) //nolint:exhaustruct // no gate is requested.
	}
}

// TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen completes the mandatory case
// through both answering surfaces: the flag, and an edit to the artefact.
func TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen(t *testing.T) {
	t.Parallel()

	for name, byEdit := range map[string]bool{"by flag": false, "by edit": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertPromotion(t, byEdit)
		})
	}
}

// assertPromotion drives one answering surface through the whole loop: open,
// answer, resume, promote, and a suite that is green as an ordinary baseline.
func assertPromotion(t *testing.T, byEdit bool) {
	t.Helper()

	module := copyFixture(t, untestedTodoFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	opened, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	resumed := characteriseConfig(t, module)
	resumed.CharacteriseWrite = true

	if byEdit {
		answerArtefact(t, filepath.Join(module, opened.Characterisation.Files[0].Path), answeredCIDR)

		resumed.Resume = true
	} else {
		resumed.Answers = []string{opened.Characterisation.Todos[0].ID + "=" + answeredCIDR}
	}

	result, err := engine.Run(t.Context(), resumed)
	if err != nil {
		t.Fatalf("characterise --resume: %v", err)
	}

	assertPromoted(t, result.Characterisation)

	written := readFile(t, filepath.Join(module, "tests", "characterise_defaults.tftest.hcl"))
	if !strings.Contains(written, "vpc_cidr = "+answeredCIDR) {
		t.Fatalf("the promoted suite does not carry the answer:\n%s", written)
	}

	// Promotion means verified: the written suite is green as an ordinary
	// baseline.
	if _, err := engine.Run(t.Context(), baseConfig(t, module)); err != nil {
		t.Fatalf("the promoted suite is not green: %v", err)
	}
}

func assertPromoted(t *testing.T, block *report.Characterisation) {
	t.Helper()

	if block.OpenTodos() != 0 {
		t.Fatalf("the answered judgement point is still open: %+v", block.Todos)
	}

	if len(block.Todos) != 1 || block.Todos[0].Status != report.TodoPromoted {
		t.Fatalf("the answered judgement point was not promoted: %+v", block.Todos)
	}

	if !block.Complete {
		t.Fatal("the resumed characterisation is incomplete")
	}
}

// TestTodosListsTheOpenJudgementPointsWithTheirEvidence is the `tf-mut todos`
// contract: the constraint verbatim, the range, the diagnostic and the values
// already tried — everything an answer needs and nothing that costs a run.
func TestTodosListsTheOpenJudgementPointsWithTheirEvidence(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedTodoFixture)

	config := characteriseConfig(t, module)
	config.Characterise = false
	config.Todos = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("todos: %v", err)
	}

	if result.Command != report.CommandTodos {
		t.Fatalf("command = %s, want todos", result.Command)
	}

	todos := result.Characterisation.Todos
	if len(todos) != 1 {
		t.Fatalf("listed %d judgement points, want one", len(todos))
	}

	if !strings.Contains(todos[0].Constraint, "cidrnetmask") {
		t.Fatalf("the listing does not carry the constraint verbatim: %q", todos[0].Constraint)
	}

	if todos[0].Range.File == "" || todos[0].Range.Start.Line == 0 {
		t.Fatalf("the listing carries no source range: %+v", todos[0].Range)
	}

	if todos[0].Diagnostic == "" {
		t.Fatal("the listing carries no diagnostic")
	}

	if len(todos[0].Attempted) == 0 {
		t.Fatal("the listing names no attempted value")
	}
}

// TestASecretInAFailedAttemptReachesNoArtefact is the M8 widening's mandatory
// fixture: the secret exists only in a failed synthesis attempt, which is
// before the pin point the earlier predicate started at.
func TestASecretInAFailedAttemptReachesNoArtefact(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedSecretFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	//nolint:gosec // the fixture's planted value, which the test proves reaches nothing.
	const secret = "tfmut-diagnostic-secret"

	rendered := strings.Builder{}
	if err := report.WriteTerminal(&rendered, result); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	artefacts := map[string]string{
		"the terminal rendering": rendered.String(),
		"the JSON report":        encoded.String(),
	}
	for _, file := range result.Characterisation.Files {
		artefacts["the generated file "+file.Path] = file.Content
	}

	for name, artefact := range artefacts {
		if strings.Contains(artefact, secret) {
			t.Fatalf("%s carries the secret from the failed attempt", name)
		}
	}

	if result.Characterisation.OpenTodos() == 0 {
		t.Fatal("the fixture produced no failed attempt")
	}
}

// answerArtefact replaces the placeholder in a non-executable artefact, which
// is exactly what a human or an agent editing the file does.
func answerArtefact(t *testing.T, path, value string) {
	t.Helper()

	content := readFile(t, path)

	replaced := strings.Replace(content, "= TFMUT_TODO", "= "+value, 1)
	if replaced == content {
		t.Fatalf("%s carries no placeholder to answer:\n%s", path, content)
	}

	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
