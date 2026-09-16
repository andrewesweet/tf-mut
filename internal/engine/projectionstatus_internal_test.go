package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// The status-projection totality proof.
//
// Splitting each context's status vocabulary from its published wire spelling
// created two places a status can be named, free to drift. This test proves
// they cannot: the projection from each context's own vocabulary onto the
// published wire spellings is total in both directions. Every context status
// has a wire spelling, and every wire spelling is reachable from a context
// status — a bijection, modulo exactly two documented deprecations, named
// below: the withdrawn `mock-masked` diagnosis and `Evidence.MockResource`,
// both retained so earlier documents stay readable and never emitted.
//
// The "every" on each side is not the test's own good faith. Each side's
// census is read mechanically from the source of truth it names — the constant
// declarations in the owning package, and the struct fields of the published
// evidence value — so a status added on either side without a counterpart
// turns this test red. The context switch statements the tables drive are
// additionally held complete by the exhaustive linter, which is the
// compile-time demand the projection comments claim.

// The packages whose vocabularies the proof holds, and the published package
// the wire spellings live in. The paths are the sibling directories the test
// binary runs from.
const (
	suggestPackage      = "../suggest"
	characterisePackage = "../characterise"
	oraclePackage       = "../oracle"
	reportPackage       = "../report"

	diagnosisVocabulary  = "Diagnosis"
	outcomeVocabulary    = "outcome"
	skipReasonVocabulary = "SkipReason"
)

// contextStatus identifies one constant of one context-owned vocabulary, so
// the handled set cannot confuse the two packages that both own a SkipReason.
type contextStatus struct {
	packageDir string
	vocabulary string
	constant   string
}

// wireReachable collects the published spellings the projections actually
// produced, per published vocabulary. The keys are the wire values themselves,
// so a produced spelling and a census entry compare without a name mapping.
type wireReachable struct {
	suggestionStatus map[string]bool
	pinStatus        map[string]bool
	todoStatus       map[string]bool
	scaffoldStatus   map[string]bool
	inputProvenance  map[string]bool
	diagnosis        map[string]bool
	evidenceField    map[string]bool
}

// TestTheStatusProjectionIsTotalInBothDirections drives every context status
// through the projection the application layer performs and holds the result
// against a mechanical census of both sides.
func TestTheStatusProjectionIsTotalInBothDirections(t *testing.T) {
	t.Parallel()

	handled := map[contextStatus]bool{}
	reachable := wireReachable{
		suggestionStatus: map[string]bool{},
		pinStatus:        map[string]bool{},
		todoStatus:       map[string]bool{},
		scaffoldStatus:   map[string]bool{},
		inputProvenance:  map[string]bool{},
		diagnosis:        map[string]bool{},
		evidenceField:    map[string]bool{},
	}

	exerciseSuggestionStatuses(t, handled, reachable)
	exercisePinStatuses(t, handled, reachable)
	exerciseTodoStatuses(t, handled, reachable)
	exerciseScaffoldStatuses(t, handled, reachable)
	exerciseInputProvenances(t, handled, reachable)
	exerciseDiagnosesAndEvidence(t, handled, reachable)

	assertEveryWireSpellingIsReachable(t, reachable)
	assertEveryContextStatusHasAWireSpelling(t, handled)
}

// exerciseSuggestionStatuses drives the Suggestion context's terminal outcomes
// and skip reasons through the suggestion projection, marking every context
// constant the table names and recording the wire spelling each produced.
func exerciseSuggestionStatuses(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	evidence := suggest.NewVerification(
		suggest.NewLeg(true, nil, "the full suite stayed green with the batch applied"),
		suggest.NewLeg(false, []suggest.RunRecord{
			suggest.NewRunRecord("tests/unit.tftest.hcl", "applied", 2, "fail"),
		}, "the suggestion alone failed against its re-materialised mutant"),
	)

	candidate := suggest.NewCandidate("0123456789ab", "tests/unit.tftest.hcl", "applied",
		`example_thing.app.input == "steady"`, "--- a/tests\n+++ b/tests")

	if projected := projectCandidate(candidate); projected.Status != report.SuggestionCandidate {
		t.Errorf("the candidate projected status %q, want %q",
			projected.Status, report.SuggestionCandidate)
	} else {
		reachable.suggestionStatus[string(report.SuggestionCandidate)] = true
	}

	verified := candidate.Verify("digest", evidence)

	if projected := projectSuggestion(verified); projected.Status != report.SuggestionVerified {
		t.Errorf("the verified outcome projected status %q, want %q",
			projected.Status, report.SuggestionVerified)
	} else {
		reachable.suggestionStatus[string(report.SuggestionVerified)] = true
	}

	refuted := candidate.Refute("why", evidence)

	if projected := projectSuggestion(refuted); projected.Status != report.SuggestionRefuted {
		t.Errorf("the refuted outcome projected status %q, want %q",
			projected.Status, report.SuggestionRefuted)
	} else {
		reachable.suggestionStatus[string(report.SuggestionRefuted)] = true
	}

	markHandled(handled, suggestPackage, outcomeVocabulary, "outcomeVerified")
	markHandled(handled, suggestPackage, outcomeVocabulary, "outcomeRefuted")

	driveSkipReasons(t, handled, reachable.suggestionStatus, suggestPackage,
		"outcomeSkipped",
		[]skipRow[suggest.SkipReason, report.SuggestionStatus]{
			{
				constant: "SkipSensitive", reason: suggest.SkipSensitive,
				want: report.SuggestionSkippedSensitive,
			},
			{
				constant: "SkipUnaddressable", reason: suggest.SkipUnaddressable,
				want: report.SuggestionSkippedUnaddressable,
			},
			{
				constant: "SkipUnrenderable", reason: suggest.SkipUnrenderable,
				want: report.SuggestionSkippedUnrenderable,
			},
			{
				constant: "SkipUnsupportedTarget", reason: suggest.SkipUnsupportedTarget,
				want: report.SuggestionSkippedUnsupportedTarget,
			},
		},
		func(reason suggest.SkipReason) report.SuggestionStatus {
			projected := projectSuggestion(suggest.Skipped(
				"0123456789ab", "tests/unit.tftest.hcl", "applied", reason, "the detail",
			))

			return projected.Status
		})
}

// exercisePinStatuses drives the Characterisation context's pin outcomes and
// pin skip reasons through the pin projection.
func exercisePinStatuses(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	driveSkipReasons(t, handled, reachable.pinStatus, characterisePackage,
		"outcomeSkipped",
		[]skipRow[characterise.SkipReason, report.PinStatus]{
			{
				constant: "SkipSensitive", reason: characterise.SkipSensitive,
				want: report.PinSkippedSensitive,
			},
			{
				constant: "SkipUnrenderable", reason: characterise.SkipUnrenderable,
				want: report.PinSkippedUnrenderable,
			},
			{
				constant: "SkipVolatile", reason: characterise.SkipVolatile,
				want: report.PinSkippedVolatile,
			},
			{
				constant: "SkipMockInvented", reason: characterise.SkipMockInvented,
				want: report.PinSkippedMockInvented,
			},
		},
		func(reason characterise.SkipReason) report.PinStatus {
			projected := projectPin(characterise.PinSkipped(
				"scenario-1", "terraform_data.app", "configured", reason, "the detail",
			))

			return projected.Status
		})

	projected := projectPin(characterise.Pinned(
		"scenario-1", "terraform_data.app", `terraform_data.app.input == "steady"`, "configured",
	))

	if projected.Status != report.Pinned {
		t.Errorf("the pinned outcome projected status %q, want %q", projected.Status, report.Pinned)
	} else {
		markHandled(handled, characterisePackage, outcomeVocabulary, "outcomePinned")
		reachable.pinStatus[string(report.Pinned)] = true
	}
}

// exerciseTodoStatuses drives the judgement point through every transition the
// context owns, so each state's wire spelling is the one the projection wrote.
func exerciseTodoStatuses(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	open := characterise.OpenTodo(characterise.TodoEvidence{
		ID: "todo-1", Variable: "token",
		Constraint: `length(var.token) == 8`,
		ConstraintRange: hcl.Range{
			Filename: "main.tf", Start: hcl.InitialPos, End: hcl.InitialPos,
		},
		File: "main.tf",
	})

	for _, row := range []struct {
		constant string
		want     report.TodoStatus
	}{
		{constant: "TodoOpen", want: report.TodoOpen},
		{constant: "TodoAnswered", want: report.TodoAnswered},
		{constant: "TodoPromoted", want: report.TodoPromoted},
		{constant: "TodoRejected", want: report.TodoRejected},
	} {
		var projected report.TodoStatus

		switch row.constant {
		case "TodoOpen":
			projected = projectTodo(open).Status
		case "TodoAnswered":
			projected = projectTodo(open.Answer().Point()).Status
		case "TodoPromoted":
			projected = projectTodo(open.Answer().Promote(characterise.Verified())).Status
		case "TodoRejected":
			projected = projectTodo(open.Answer().Reject("the answer failed verification")).Status
		default:
			t.Fatalf("the forward table drives an unknown judgement point state %q", row.constant)
		}

		if projected != row.want {
			t.Errorf("the judgement point state %s projected status %q, want %q",
				row.constant, projected, row.want)
		}

		markHandled(handled, characterisePackage, "TodoStatus", row.constant)
		reachable.todoStatus[string(row.want)] = true
	}
}

// exerciseScaffoldStatuses drives the construct scaffold through both of its
// states and the promotion transition that separates them.
func exerciseScaffoldStatuses(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	scaffold := characterise.Scaffolded("check.block", "tests/scaffold.tftest.hcl")

	if projected := projectScaffold(scaffold); projected.Status != report.Scaffolded {
		t.Errorf("the scaffolded state projected status %q, want %q",
			projected.Status, report.Scaffolded)
	} else {
		markHandled(handled, characterisePackage, "ScaffoldStatus", "StatusScaffolded")
		reachable.scaffoldStatus[string(report.Scaffolded)] = true
	}

	promoted := scaffold.Promote(characterise.Verified())

	if projected := projectScaffold(promoted); projected.Status != report.ScaffoldPromoted {
		t.Errorf("the promoted state projected status %q, want %q",
			projected.Status, report.ScaffoldPromoted)
	} else {
		markHandled(handled, characterisePackage, "ScaffoldStatus", "StatusPromoted")
		reachable.scaffoldStatus[string(report.ScaffoldPromoted)] = true
	}
}

// exerciseInputProvenances drives the synthesis preference order's vocabulary
// through the input projection.
func exerciseInputProvenances(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	for _, row := range []struct {
		constant   string
		provenance characterise.InputProvenance
		want       report.InputProvenance
	}{
		{
			constant: "FromDefault", provenance: characterise.FromDefault,
			want: report.FromDefault,
		},
		{
			constant: "FromValidation", provenance: characterise.FromValidation,
			want: report.FromValidation,
		},
		{
			constant: "FromType", provenance: characterise.FromType,
			want: report.FromType,
		},
		{
			constant: "FromAnswer", provenance: characterise.FromAnswer,
			want: report.FromAnswer,
		},
	} {
		input := characterise.Synthesis{
			Name: "region", Expression: `"eu-west-1"`, Provenance: row.provenance,
		}.Input(discovery.Block{Name: "region"})

		projected := projectInputs([]characterise.Input{input})

		if projected[0].Provenance != row.want {
			t.Errorf("the input provenance %s projected provenance %q, want %q",
				row.constant, projected[0].Provenance, row.want)
		}

		markHandled(handled, characterisePackage, "InputProvenance", row.constant)
		reachable.inputProvenance[string(row.want)] = true
	}
}

// exerciseDiagnosesAndEvidence drives every survivor constructor the Oracle
// context owns through the mutant projection, so each diagnosis's wire
// spelling and each evidence field the constructors write are recorded.
func exerciseDiagnosesAndEvidence(
	t *testing.T, handled map[contextStatus]bool, reachable wireReachable,
) {
	t.Helper()

	mask := fingerprint.NewMask()
	mask.Spans["resources.terraform_data.app.id"] = fingerprint.SyntaxSpan("", "", true)
	delta := fingerprint.Delta{Changes: []fingerprint.Change{{
		Run: "tests/unit.tftest.hcl::applied", Path: "outputs.tier.value",
		Address: "output.tier", Baseline: `"1"`, Mutant: `"2"`,
	}}}
	read := discovery.Reach{Read: true, Assertion: discovery.Assertion{
		File: "tests/unit.tftest.hcl", Run: "applied", Line: 3,
	}}
	defeated := discovery.Reach{Defeated: true, Assertion: discovery.Assertion{
		File: "tests/unit.tftest.hcl", Run: "applied", Line: 7,
	}, Construct: "for_each"}

	for _, row := range []struct {
		constant string
		outcome  oracle.Outcome
		want     report.Diagnosis
	}{
		{
			constant: "IndeterminateUnknownValues",
			want:     report.IndeterminateUnknownValues,
			outcome: oracle.SurvivedIndeterminateUnknowns(
				[]string{"outputs.tier.value"}, mask,
			),
		},
		{
			constant: "IndeterminateVolatility",
			want:     report.IndeterminateVolatility,
			outcome: oracle.SurvivedIndeterminateVolatility(
				delta, mask, []string{"terraform_data.app.output"},
			),
		},
		{
			constant: "WeakAssertion",
			want:     report.WeakAssertion,
			outcome:  oracle.SurvivedWeakAssertion(delta, mask, "terraform_data.app", read),
		},
		{
			constant: "NoAssertion",
			want:     report.NoAssertion,
			outcome:  oracle.SurvivedNoAssertion(delta, mask),
		},
		{
			constant: "Unasserted",
			want:     report.Unasserted,
			outcome:  oracle.SurvivedUnasserted(delta, mask, "terraform_data.app", defeated),
		},
	} {
		projected := project(report.Mutant{ID: "0123456789ab"}, row.outcome)

		if projected.Verdict == nil || projected.Verdict.Diagnosis != row.want {
			t.Errorf("the %s survivor projected verdict %+v, want diagnosis %q",
				row.constant, projected.Verdict, row.want)

			continue
		}

		markHandled(handled, oraclePackage, diagnosisVocabulary, row.constant)
		reachable.diagnosis[string(row.want)] = true

		for field := range evidenceFieldsWritten(projected.Verdict.Evidence) {
			reachable.evidenceField[field] = true
		}
	}
}

// skipRow is one forward table row: the context-owned constant, the value the
// context carries, and the wire spelling the projection must give it.
type skipRow[reasonT, wireT ~string] struct {
	constant string
	reason   reasonT
	want     wireT
}

// driveSkipReasons drives one skip vocabulary's rows through its projection
// and records both directions' bookkeeping: the context constants the table
// named, and the wire spellings the projection produced.
func driveSkipReasons[reasonT, wireT ~string](
	t *testing.T,
	handled map[contextStatus]bool,
	reachable map[string]bool,
	packageDir, outcomeConstant string,
	rows []skipRow[reasonT, wireT],
	project func(reason reasonT) wireT,
) {
	t.Helper()

	for _, row := range rows {
		if got := project(row.reason); got != row.want {
			t.Errorf("the skip reason %s projected status %q, want %q",
				row.constant, got, row.want)
		}

		markHandled(handled, packageDir, skipReasonVocabulary, row.constant)
		markHandled(handled, packageDir, outcomeVocabulary, outcomeConstant)
		reachable[string(row.want)] = true
	}
}

// assertEveryWireSpellingIsReachable is the reverse direction: every constant
// of every published status vocabulary is reachable from a context status,
// except the two documented deprecations, which this also proves are never
// emitted.
func assertEveryWireSpellingIsReachable(t *testing.T, reachable wireReachable) {
	t.Helper()

	// The two documented deprecations, the only exemptions to the reverse
	// direction, each named with its own justification. Both stay declared so
	// earlier documents remain readable; neither is ever emitted.
	exemptions := map[string]map[string]string{
		diagnosisVocabulary: {
			"MockMasked": "withdrawn (M3, issue #50): its positive case cannot fire, and the " +
				"oracle never emits it again; the value stays declared so the 2.1.0 schema " +
				"remains additive over 2.0 documents",
		},
		"EvidenceField": {
			"MockResource": "carried only by the withdrawn mock-masked diagnosis; the field " +
				"stays declared so 2.0 documents remain readable, and no context carries it",
		},
	}

	for _, side := range []struct {
		vocabulary string
		reachable  map[string]bool
	}{
		{vocabulary: "SuggestionStatus", reachable: reachable.suggestionStatus},
		{vocabulary: "PinStatus", reachable: reachable.pinStatus},
		{vocabulary: "TodoStatus", reachable: reachable.todoStatus},
		{vocabulary: "ScaffoldStatus", reachable: reachable.scaffoldStatus},
		{vocabulary: "InputProvenance", reachable: reachable.inputProvenance},
		{vocabulary: diagnosisVocabulary, reachable: reachable.diagnosis},
	} {
		for name, value := range vocabularyCensus(t, reportPackage, side.vocabulary) {
			reason, exempt := exemptions[side.vocabulary][name]
			if exempt {
				if side.reachable[value] {
					t.Errorf("the withdrawn spelling %s.%s was emitted, but the deprecation "+
						"never is: %s", side.vocabulary, name, reason)
				}

				continue
			}

			if !side.reachable[value] {
				t.Errorf("the wire spelling %s.%s (%q) is reachable from no context status: "+
					"extend the owning context's vocabulary and the projection, or record "+
					"the deprecation in this test's exemptions with its justification",
					side.vocabulary, name, value)
			}
		}
	}

	evidenceType := reflect.TypeFor[report.Evidence]()
	for index := range evidenceType.NumField() {
		field := evidenceType.Field(index).Name
		reason, exempt := exemptions["EvidenceField"][field]
		if exempt {
			if reachable.evidenceField[field] {
				t.Errorf("the withdrawn evidence field Evidence.%s was written, but the "+
					"deprecation never is: %s", field, reason)
			}

			continue
		}

		if !reachable.evidenceField[field] {
			t.Errorf("the published evidence field Evidence.%s is written by no context "+
				"evidence: extend the Oracle context's evidence and the projection, or "+
				"record the deprecation in this test's exemptions", field)
		}
	}
}

// assertEveryContextStatusHasAWireSpelling is the forward direction: every
// constant of every context-owned status vocabulary is named by the forward
// tables, each of which asserted the wire spelling the projection gives it.
func assertEveryContextStatusHasAWireSpelling(t *testing.T, handled map[contextStatus]bool) {
	t.Helper()

	for _, side := range []struct {
		packageDir string
		vocabulary string
	}{
		{packageDir: suggestPackage, vocabulary: skipReasonVocabulary},
		{packageDir: suggestPackage, vocabulary: outcomeVocabulary},
		{packageDir: characterisePackage, vocabulary: skipReasonVocabulary},
		{packageDir: characterisePackage, vocabulary: "TodoStatus"},
		{packageDir: characterisePackage, vocabulary: "ScaffoldStatus"},
		{packageDir: characterisePackage, vocabulary: "InputProvenance"},
		{packageDir: characterisePackage, vocabulary: outcomeVocabulary},
		{packageDir: oraclePackage, vocabulary: diagnosisVocabulary},
	} {
		for constant := range vocabularyCensus(t, side.packageDir, side.vocabulary) {
			key := contextStatus{
				packageDir: side.packageDir, vocabulary: side.vocabulary, constant: constant,
			}

			if !handled[key] {
				t.Errorf("the context status %s.%s has no wire spelling: extend the "+
					"projection and name the constant in this test's forward table",
					side.vocabulary, constant)
			}
		}
	}
}

// vocabularyCensus reads one status vocabulary's source of truth: the constant
// declarations of the named type in the named package's non-test sources,
// mapped from each constant's name to its declared value. The census is
// mechanical so that a constant added without this test's knowledge turns the
// totality proof red instead of passing vacuously.
func vocabularyCensus(t *testing.T, packageDir, typeName string) map[string]string {
	t.Helper()

	declared := map[string]string{}

	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", packageDir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(
			token.NewFileSet(), filepath.Join(packageDir, name), nil, parser.SkipObjectResolution,
		)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}

		collectVocabulary(t, file, typeName, declared)
	}

	if len(declared) == 0 {
		t.Fatalf("the census of %s under %s found no constants, so it is reading the "+
			"wrong source and proves nothing", typeName, packageDir)
	}

	return declared
}

// collectVocabulary walks one parsed file's constant declarations. Within a
// const group, a spec without its own type inherits the previous spec's, which
// is how the vocabularies declare their tails after an iota-led first entry.
func collectVocabulary(
	t *testing.T, file *ast.File, typeName string, declared map[string]string,
) {
	t.Helper()

	for _, declaration := range file.Decls {
		group, isConst := declaration.(*ast.GenDecl)
		if !isConst || group.Tok != token.CONST {
			continue
		}

		inherited := ""

		for _, spec := range group.Specs {
			valueSpec, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}

			specType := inherited
			if ident, declaredHere := valueSpec.Type.(*ast.Ident); declaredHere {
				specType = ident.Name
				inherited = specType
			}

			if specType != typeName {
				continue
			}

			for index, constant := range valueSpec.Names {
				declared[constant.Name] = declaredValue(valueSpec.Values, index)
			}
		}
	}
}

// literalValue reads the string literal one constant is declared with, by its
// position in the spec, so a grouped declaration maps each name to its own
// value. An empty result is the honest answer for a constant declared without
// a literal of its own — the outcome vocabularies count by iota, and the
// proof holds them by name alone.
func declaredValue(values []ast.Expr, position int) string {
	if position >= len(values) {
		return ""
	}

	if literal, isLiteral := values[position].(*ast.BasicLit); isLiteral {
		return strings.Trim(literal.Value, "`\"")
	}

	return ""
}

// evidenceFieldsWritten names the published evidence fields one projected
// verdict actually carries, so the field census can be held against what the
// constructors write rather than against a hand-maintained list.
func evidenceFieldsWritten(evidence report.Evidence) map[string]bool {
	written := map[string]bool{}

	value := reflect.ValueOf(evidence)
	for index := range value.NumField() {
		field := value.Field(index)
		if !field.IsZero() && (field.Kind() != reflect.Slice || field.Len() > 0) {
			written[value.Type().Field(index).Name] = true
		}
	}

	return written
}

// markHandled records that the forward tables named one context constant.
func markHandled(handled map[contextStatus]bool, packageDir, vocabulary, constant string) {
	handled[contextStatus{
		packageDir: packageDir, vocabulary: vocabulary, constant: constant,
	}] = true
}
