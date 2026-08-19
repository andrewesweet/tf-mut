package engine_test

import (
	"encoding/json"
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
	untestedMinedFixture  = "untested-mined"

	untestedSensitiveAnswerFixture = "untested-sensitive-answer"

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

// TestAMinedValidationResolvesAnInputWithNoDefault covers the middle rung of
// the preference order, which the corpus measurement showed is reached rarely
// and fires rarely — a rung nothing exercised would be a rung nobody could
// tell was broken.
func TestAMinedValidationResolvesAnInputWithNoDefault(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedMinedFixture)

	result, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	block := result.Characterisation
	if block.OpenTodos() != 0 {
		t.Fatalf("a minable constraint still produced a judgement point: %+v", block.Todos)
	}

	mined := false

	for _, scenario := range block.Scenarios {
		for _, input := range scenario.Inputs {
			if input.Name != "tier" {
				continue
			}

			if input.Provenance != report.FromValidation {
				t.Fatalf("tier resolved by %s, want the validation it is named in",
					input.Provenance)
			}

			if input.Expression != `"bronze"` {
				t.Fatalf("mined %s, want the first legal value the constraint names",
					input.Expression)
			}

			mined = true
		}
	}

	if !mined {
		t.Fatal("no scenario carried the mined assignment")
	}

	if !strings.Contains(block.Files[0].Content, `output.network == "bronze"`) {
		t.Fatalf("the mined value was not characterised:\n%s", block.Files[0].Content)
	}
}

// TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure is the other half
// of the verification loop's safety property: nothing an agent supplies is
// trusted, and the failure mode of a wrong answer is a reported, attributed
// finding rather than a corrupted suite — or a stack trace.
func TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedTodoFixture)

	config := characteriseConfig(t, module)
	config.CharacteriseWrite = true

	opened, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("characterise --write: %v", err)
	}

	answered := characteriseConfig(t, module)
	answered.Answers = []string{opened.Characterisation.Todos[0].ID + `="not-a-cidr-block"`}

	result, err := engine.Run(t.Context(), answered)
	if err != nil {
		t.Fatalf("a refuted answer must be a finding, not an operational failure: %v", err)
	}

	block := result.Characterisation
	if len(block.Todos) != 1 || block.Todos[0].Status != report.TodoRejected {
		t.Fatalf("the refuted answer was not rejected: %+v", block.Todos)
	}

	if block.Todos[0].Diagnostic == "" {
		t.Fatal("the rejected answer carries no diagnostic to act on")
	}

	if block.Complete || len(block.Files) != 1 || block.Files[0].Executable {
		t.Fatalf("a refuted answer produced executable content: %+v", block.Files)
	}

	if result.ExitCode(report.Gate{}) != report.ExitFindings { //nolint:exhaustruct // no gate is requested.
		t.Fatalf("exit code = %d, want one while the judgement point still needs an answer",
			result.ExitCode(report.Gate{})) //nolint:exhaustruct // no gate is requested.
	}
}

// TestASensitiveAnswerIsVerifiedAndStillWithheld holds both halves of a
// contract that pulls in opposite directions: a secret must reach no artefact,
// and Terraform cannot plan a redaction marker. One string for both meant a
// generated run block carrying `token = (sensitive value withheld)` and every
// sensitive answer refused as unparseable.
func TestASensitiveAnswerIsVerifiedAndStillWithheld(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, untestedSensitiveAnswerFixture)

	opened, err := engine.Run(t.Context(), characteriseConfig(t, module))
	if err != nil {
		t.Fatalf("characterise: %v", err)
	}

	// The answer the fixture's constraint names.
	const secret = `"tok-0123abcd"`

	answered := characteriseConfig(t, module)
	answered.CharacteriseWrite = true
	answered.Answers = []string{opened.Characterisation.Todos[0].ID + "=" + secret}

	result, err := engine.Run(t.Context(), answered)
	if err != nil {
		t.Fatalf("a sensitive answer must verify like any other: %v", err)
	}

	block := result.Characterisation
	if block.OpenTodos() != 0 {
		t.Fatalf("the sensitive answer was not accepted: %+v", block.Todos)
	}

	if !block.Complete {
		t.Fatal("the characterisation is incomplete after a verified answer")
	}

	for _, scenario := range block.Scenarios {
		for _, input := range scenario.Inputs {
			if input.Expression != report.SensitiveWithheld {
				t.Fatalf("the report carries the sensitive assignment: %s", input.Expression)
			}
		}
	}

	encoded := strings.Builder{}
	if err := report.WriteJSON(&encoded, result); err != nil {
		t.Fatalf("encoding: %v", err)
	}

	if strings.Contains(encoded.String(), "tok-0123abcd") {
		t.Fatal("the JSON report carries the answered secret")
	}

	// The report's view of the generated file is redacted too, so the value
	// Terraform has to plan is only visible on disk — which is the whole point
	// of keeping the two renderings apart.
	for _, file := range block.Files {
		if strings.Contains(file.Content, "tok-0123abcd") {
			t.Fatalf("the reported content of %s carries the secret", file.Path)
		}
	}

	written := readFile(t, filepath.Join(module, "tests", "characterise_defaults.tftest.hcl"))
	if !strings.Contains(written, "token = "+secret) {
		t.Fatalf("the written suite does not carry the value Terraform has to plan:\n%s", written)
	}
}

// TestNoReportFieldVariesWithTheSensitiveAnswer closes the disclosure the
// withholding test could not see.
//
// A report that carries no plaintext can still disclose one. The generated
// file's digest covered the *executable* bytes while its content field carried
// the deterministic redacted template, which makes the pair an offline equality
// oracle: substitute a candidate into the template, hash, compare. The
// mandatory fixture's constraint admits `tok-[0-9a-f]{8}` — thirty-two bits.
//
// The property that closes it is stronger than "no plaintext": no published
// field may vary with the secret at all. Two different valid answers must
// produce byte-identical reports.
func TestNoReportFieldVariesWithTheSensitiveAnswer(t *testing.T) {
	t.Parallel()

	rendered := map[string]string{}

	for _, secret := range []string{`"tok-0123abcd"`, `"tok-fedc9876"`} {
		module := copyFixture(t, untestedSensitiveAnswerFixture)

		opened, err := engine.Run(t.Context(), characteriseConfig(t, module))
		if err != nil {
			t.Fatalf("characterise: %v", err)
		}

		answered := characteriseConfig(t, module)
		answered.Answers = []string{opened.Characterisation.Todos[0].ID + "=" + secret}

		result, err := engine.Run(t.Context(), answered)
		if err != nil {
			t.Fatalf("a sensitive answer must verify like any other: %v", err)
		}

		encoded := strings.Builder{}
		if err := report.WriteJSON(&encoded, result); err != nil {
			t.Fatalf("encoding: %v", err)
		}

		rendered[secret] = redactVolatile(encoded.String())
	}

	if rendered[`"tok-0123abcd"`] != rendered[`"tok-fedc9876"`] {
		t.Fatal("a published field varies with the sensitive answer, so the report " +
			"distinguishes secrets it is meant to withhold")
	}
}

// redactVolatile removes the fields that differ between any two runs, so that
// what is compared is the report's dependence on its inputs and not on the
// clock or the filesystem.
func redactVolatile(document string) string {
	decoded := map[string]any{}
	if json.Unmarshal([]byte(document), &decoded) != nil {
		return document
	}

	for _, volatile := range []string{
		"started_at", "finished_at", "duration_ms", "module", "closure_root",
	} {
		delete(decoded, volatile)
	}

	reduced, err := json.Marshal(decoded)
	if err != nil {
		return document
	}

	return string(reduced)
}
