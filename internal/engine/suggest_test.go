package engine_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// M4a: generation behind the three fail-closed adapters, through the engine
// seam. The adapter matrices themselves live in `internal/suggest`, over the
// payload paths and provider types they are contracts about; what is asserted
// here is that each of their outcomes reaches the report where the matrix says
// it must, and that no artefact of a skipped suggestion says more than it may.

const (
	suggestBasicFixture           = "suggest-basic"
	suggestSensitiveFixture       = "suggest-sensitive"
	suggestJSONFixture            = "suggest-json-target"
	suggestMultiFixture           = "suggest-multifile"
	suggestBatchFixture           = "suggest-batch"
	suggestCountFixture           = "suggest-count"
	suggestOutputList             = "suggest-output-list"
	suggestMapKeyFixture          = "suggest-map-key"
	suggestUnknownObjectFixture   = "suggest-unknown-object"
	suggestCountDeletionFixture   = "suggest-count-deletion"
	suggestCountZeroFixture       = "suggest-count-zero"
	suggestCollapseCarrierFixture = "suggest-collapse-carrier"
	suggestEmptyCollectionFixture = "suggest-empty-collection"
	suggestOutputMetadataFixture  = "suggest-output-metadata"
	secretValue                   = "s3cret-value"
	unitTestFile                  = "tests/unit.tftest.hcl"
)

// dryRunRequest is a suggest run that generates and verifies nothing.
func dryRunRequest(t *testing.T, module string) engine.SuggestRequest {
	t.Helper()

	request := suggestRequest(t, module)
	request.DryRun = true

	return request
}

func runSuggest(t *testing.T, request engine.SuggestRequest) report.Report {
	t.Helper()

	result, err := engine.Run(t.Context(), request)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}

	return result
}

// The map-key fixture: a null_resource map attribute whose key is a dotted
// Kubernetes label. The payload path flattens that key across several
// dot-separated path segments, and only the provider schema's map type can
// tell the key from a nested attribute — every value mutation under it used
// to be refused, and now renders in index form. The container's own nulling
// mutants are answered by the non-null form, which is the suggestion this
// fixture verifies.
func TestANonIdentifierMapKeyIsRenderedInIndexForm(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("the real-Terraform run takes over nine seconds; -short defers it to `just test`")
	}

	requireProviderMirror(t)

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestMapKeyFixture)))

	for _, skipped := range result.Suggestions {
		switch skipped.Status { //nolint:exhaustive // a guard: only the skip statuses are defects here.
		case report.SuggestionSkippedUnaddressable, report.SuggestionSkippedUnrenderable:
			t.Fatalf("a map-key survivor was skipped as %s: %s",
				skipped.Status, skipped.StatusReason)
		default:
		}
	}

	const dotted = `try(null_resource.app.triggers["app.kubernetes.io/part-of"], null) == "tf-mut-observe"`
	var sawIndexForm, sawNonNull bool
	for _, suggestion := range result.Suggestions {
		switch {
		case suggestion.Expression == dotted:
			sawIndexForm = true
		case suggestion.Expression == "try(null_resource.app.triggers, null) != null" &&
			suggestion.Status == report.SuggestionVerified:
			sawNonNull = true
		default:
		}
	}

	if !sawIndexForm {
		t.Fatalf("no suggestion carries %q; statuses were %s",
			dotted, statusSummary(result.Suggestions))
	}

	if !sawNonNull {
		t.Fatalf("no verified suggestion renders the map's non-null form; statuses were %s",
			statusSummary(result.Suggestions))
	}
}

// The list-valued output fixture: mutations of the output's element and a
// mutant that nulls the whole output survive with deltas the payload carries
// as `outputs.region_list.value[0].short_name` and friends. Before the address
// adapter carried the element index, every one of these was skipped as
// unaddressable or unrenderable; now the element assertion is generated, and
// the metadata paths in the same delta — the type descriptor — are never what
// the suggestion comes from.
func TestAListValuedOutputElementIsAddressedThroughItsIndex(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestOutputList)))

	const want = `try(output.region_list[0].short_name, null) == "eu"`
	for _, skipped := range result.Suggestions {
		switch skipped.Status { //nolint:exhaustive // a guard: only the skip statuses are defects here.
		case report.SuggestionSkippedUnaddressable, report.SuggestionSkippedUnrenderable:
			t.Fatalf("a survivor of the list-valued output was skipped as %s: %s",
				skipped.Status, skipped.StatusReason)
		case report.SuggestionSkippedSensitive:
			t.Fatalf("a survivor of the list-valued output was skipped as sensitive: %s",
				skipped.StatusReason)
		default:
		}
	}

	for _, suggestion := range result.Suggestions {
		if suggestion.Expression != want {
			continue
		}

		if suggestion.TargetFile != unitTestFile || suggestion.TargetRun != "applied" {
			t.Fatalf("placement = %s:%s, want the run that carried the delta",
				suggestion.TargetFile, suggestion.TargetRun)
		}

		return
	}

	t.Fatalf("no suggestion carries %q; statuses were %s",
		want, statusSummary(result.Suggestions))
}

// The unknown-object fixture: an object output one of whose members is an
// unknown attribute value, observed under a plan run. The baseline payload
// marks that member in the output's after_unknown record while the value
// itself holds members — the shape a length assertion reads as an empty
// collection and answers with a statement that is false on the baseline. The
// nulling mutant is answered by the non-null form instead.
func TestAnUnknownObjectOutputAssertsNonNullAndNeverALength(t *testing.T) {
	t.Parallel()
	requireProviderMirror(t)

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestUnknownObjectFixture)))

	sawNonNull := false
	for _, suggestion := range result.Suggestions {
		if strings.Contains(suggestion.Expression, "length(output.identity)") {
			t.Fatal("a length was rendered over a value whose members the plan marks unknown")
		}

		if suggestion.Expression == "output.identity != null" &&
			suggestion.Status == report.SuggestionVerified {
			if suggestion.TargetRun != "planned" {
				t.Fatalf("placement = %s:%s, want the plan run that carried the delta",
					suggestion.TargetFile, suggestion.TargetRun)
			}

			sawNonNull = true
		}
	}

	if !sawNonNull {
		t.Fatalf("no verified suggestion renders the output's non-null form; statuses were %s",
			statusSummary(result.Suggestions))
	}
}

// The count fixture: a resource whose count is two. A count mutation removes
// an instance, and the vanishing instance's payload paths carry values on
// their baseline side alone — evidence of how large the baseline collection
// was. The suggestion is a length equality over the un-indexed resource,
// which is false on the mutant and kills it, where an assertion against the
// vanished instance itself would be refused by Terraform as an invalid index
// and a run error is no kill.
func TestAVanishedInstanceIsAssertedAsAResourceCount(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestCountDeletionFixture)))

	for _, suggestion := range result.Suggestions {
		if suggestion.Expression == "length(terraform_data.counted) == 2" &&
			suggestion.Status == report.SuggestionVerified {
			return
		}
	}

	t.Fatalf("no verified suggestion renders the resource's count; statuses were %s",
		statusSummary(result.Suggestions))
}

// The zero-count fixture: a zero-count mutation keeps the resource's
// declaration and removes every instance, so the delta's instance records —
// each one's address and index among them — prove the baseline collection was
// two. The length equality the evidence renders is true on the baseline and
// false on the mutant, where an assertion against a vanished instance itself
// would be refused by Terraform as an invalid index and a run error is no
// kill. The suggestion is judged against the zeroing mutant alone, whose own
// delta feeds the count form and whose verification legs both hold.
func TestAZeroCountMutationIsAssertedAsAResourceCount(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"apply", "plan"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			moduleDir := copyFixture(t, suggestCountZeroFixture)
			testContent := fmt.Sprintf("run %q {\n  command = %s\n}\n", command, command)
			if err := os.WriteFile(filepath.Join(moduleDir, unitTestFile), []byte(testContent), 0o600); err != nil {
				t.Fatalf("writing %s suite: %v", command, err)
			}

			mutation, err := engine.Run(t.Context(), previewRequest(t, moduleDir))
			if err != nil {
				t.Fatalf("preview: %v", err)
			}

			zeroed := mutantsWithOperator(mutation, "EXT-RESOURCE-DELETE")
			if len(zeroed) != 1 {
				t.Fatalf("expected exactly one zero-count mutant, got %d", len(zeroed))
			}

			request := suggestRequest(t, moduleDir)
			request.SurvivorIDs = []string{zeroed[0].ID}

			result := runSuggest(t, request)

			for _, suggestion := range result.Suggestions {
				if suggestion.Expression == "length(terraform_data.zeroed) == 2" &&
					suggestion.Status == report.SuggestionVerified {
					if suggestion.TargetRun != command {
						t.Fatalf("placement = %s:%s, want the %s run carrying the delta",
							suggestion.TargetFile, suggestion.TargetRun, command)
					}

					return
				}
			}

			t.Fatalf("no verified suggestion renders the resource's count; statuses were %s",
				statusSummary(result.Suggestions))
		})
	}
}

func TestSuggestGeneratesTheAssertionThatWouldHaveKilledASurvivor(t *testing.T) {
	t.Parallel()

	request := suggestRequest(t, copyFixture(t, suggestBasicFixture))
	request.DryRun = true

	// The non-nil pointer is the #96 pointer-boundary proof for this command:
	// the engine accepts the request type by value or by pointer alike.
	result, err := engine.Run(t.Context(), &request)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}

	candidates := withStatus(result, report.SuggestionCandidate)
	if len(candidates) == 0 {
		t.Fatalf("no candidate was generated; statuses were %s", statusSummary(result.Suggestions))
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

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestBasicFixture)))

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

	first := identifiersOf(runSuggest(t, dryRunRequest(t, module)))
	second := identifiersOf(runSuggest(t, dryRunRequest(t, module)))

	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("identifiers changed across runs:\n%v\n%v", first, second)
	}

	testsupport.WriteFile(t, filepath.Join(module, "unrelated.tf"),
		"resource \"terraform_data\" \"unrelated\" {\n  input = \"elsewhere\"\n}\n")

	third := identifiersOf(runSuggest(t, dryRunRequest(t, module)))
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

			result := runSuggest(t, dryRunRequest(t, copyFixture(t, fixture)))

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

				if mutant.Verdict.Diagnosis == report.IndeterminateUnknownValues ||
					mutant.Verdict.Diagnosis == report.IndeterminateVolatility {
					t.Fatalf("mutant %s is %s and still received a suggestion",
						mutant.ID, mutant.Verdict.Diagnosis)
				}
			}
		})
	}
}

func TestAJSONTestTargetIsSkippedWithNoPatch(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestJSONFixture)))

	skipped := withStatus(result, report.SuggestionSkippedUnsupportedTarget)
	if len(skipped) == 0 {
		t.Fatalf("a JSON-declared run produced no unsupported-target skip: %s",
			statusSummary(result.Suggestions))
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

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestSensitiveFixture)))

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
		suggestOutputMetadataFixture,
	} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			result := runSuggest(t, dryRunRequest(t, copyFixture(t, fixture)))

			for _, suggestion := range result.Suggestions {
				assertPresenceRules(t, suggestion)
			}
		})
	}
}

// TestAnOutputMetadataOnlyDeltaIsSkippedAsOutputMetadata is the D6b
// reproduction: a mutant that flips an output's sensitive flag changes only
// the output's sensitivity metadata, which no `terraform test` assertion
// addresses, so the suggestion is an output-metadata skip with its own
// reason rather than a generic unaddressable one.
func TestAnOutputMetadataOnlyDeltaIsSkippedAsOutputMetadata(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestOutputMetadataFixture)))

	operators := mutantOperators(result)

	found := false
	for _, suggestion := range result.Suggestions {
		if operators[suggestion.MutantID] != "OUT-SENSITIVE-FLIP" {
			continue
		}

		found = true

		if suggestion.Status != report.SuggestionSkippedOutputMetadata {
			t.Fatalf("sensitive-flag survivor is %s, want %s: %s",
				suggestion.Status, report.SuggestionSkippedOutputMetadata, suggestion.StatusReason)
		}

		if !suggestion.Status.Skipped() {
			t.Fatalf("status %s does not report as skipped", suggestion.Status)
		}

		if !strings.Contains(suggestion.StatusReason, "no output value difference exists") {
			t.Fatalf("metadata-only reason does not explain the unchanged value: %s",
				suggestion.StatusReason)
		}

		assertPresenceRules(t, suggestion)
	}

	if !found {
		t.Fatalf("the sensitive-flag survivor received no suggestion: %s",
			statusSummary(result.Suggestions))
	}
}

// TestAValueDeltaOutranksAnOutputsTypeMetadata pins the D6b preference: the
// key-drop mutant changes the object value and rewrites the output's type
// descriptor with it, so the suggestion asserts on the value.
func TestAValueDeltaOutranksAnOutputsTypeMetadata(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, dryRunRequest(t, copyFixture(t, suggestOutputMetadataFixture)))

	operators := mutantOperators(result)

	found := false
	for _, suggestion := range result.Suggestions {
		if operators[suggestion.MutantID] != "COLL-DROP-ENTRY" {
			continue
		}

		found = true

		if suggestion.Status != report.SuggestionCandidate {
			t.Fatalf("key-drop survivor is %s, want a candidate: %s",
				suggestion.Status, suggestion.StatusReason)
		}

		if suggestion.Expression != `try(output.config.name, null) == "app"` {
			t.Fatalf("expression = %q, want the output value assertion", suggestion.Expression)
		}
	}

	if !found {
		t.Fatalf("the key-drop survivor received no suggestion: %s",
			statusSummary(result.Suggestions))
	}
}

func TestOutputValueSelectionUsesCompleteDelta(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		fixture  string
		operator string
		run      string
	}{
		{fixture: "suggest-output-large-tuple", operator: "COLL-EMPTY", run: "applied"},
		{fixture: "suggest-output-later-run", operator: "COND-TRUE", run: "second"},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			t.Parallel()

			request := dryRunRequest(t, copyFixture(t, test.fixture))
			request.Tier = "standard"
			request.IncludeOperators = []string{test.operator}

			for _, stage := range []string{"fresh", "cached", "verified"} {
				request.DryRun = stage != "verified"
				result := runSuggest(t, request)
				if len(result.Suggestions) != 1 {
					t.Fatalf("%s: want one suggestion, got %s", stage, statusSummary(result.Suggestions))
				}

				suggestion := result.Suggestions[0]
				wantStatus := report.SuggestionCandidate
				if stage == "verified" {
					wantStatus = report.SuggestionVerified
				}

				if suggestion.Status != wantStatus || suggestion.Expression != `try(output.aliases[0], null) == "x"` {
					t.Fatalf("%s: suggestion = %s %q (%s), want %s with the baseline element assertion",
						stage, suggestion.Status, suggestion.Expression, suggestion.StatusReason, wantStatus)
				}

				if suggestion.TargetFile != unitTestFile || suggestion.TargetRun != test.run {
					t.Fatalf("%s: placement = %s:%s, want %s:%s", stage,
						suggestion.TargetFile, suggestion.TargetRun, unitTestFile, test.run)
				}

				if stage != "fresh" && result.Population.Cached != 1 {
					t.Fatalf("%s: want the survivor replayed from cache, got %d cache hits",
						stage, result.Population.Cached)
				}

				if test.operator == "COLL-EMPTY" {
					mutant, found := result.MutantByID(suggestion.MutantID)
					if !found || mutant.Verdict == nil || len(mutant.Verdict.Evidence.Delta) != 20 {
						t.Fatalf("%s: the report must retain its 20-change display cap", stage)
					}

					for _, change := range mutant.Verdict.Evidence.Delta {
						if strings.Contains(change.Path, ".value") {
							t.Fatalf("%s: expected type metadata to fill the display delta, got %s", stage, change.Path)
						}
					}
				}
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

// statusSummary renders a suggestion set as its status counts, in vocabulary
// order, for assertion messages. It is the engine tests' own helper since the
// Suggestion context stopped reading the report DTO.
func statusSummary(suggestions []report.Suggestion) string {
	counts := map[report.SuggestionStatus]int{}
	for _, suggestion := range suggestions {
		counts[suggestion.Status]++
	}

	parts := []string{}

	for _, status := range []report.SuggestionStatus{
		report.SuggestionVerified, report.SuggestionCandidate, report.SuggestionRefuted,
		report.SuggestionSkippedSensitive, report.SuggestionSkippedUnaddressable,
		report.SuggestionSkippedUnrenderable, report.SuggestionSkippedUnsupportedTarget,
	} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}

	return strings.Join(parts, ", ")
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

	for name, config := range map[string]engine.SuggestRequest{
		"verified": suggestRequest(t, copyFixture(t, suggestBasicFixture)),
		"skips":    dryRunRequest(t, copyFixture(t, suggestSensitiveFixture)),
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

// TestACollapsedCarrierAcrossAbsenceAndValueMutantsVerifies is the D13/D14
// reproduction: the utl-regions `locals.tf:12` shape — one assertion folds an
// absence-operator carrier and value-change members, and the carrier deletes
// what the assertion reads. The suggestion verifies with every listed
// mutant's leg in its evidence, and the cost statement counts every leg.
func TestACollapsedCarrierAcrossAbsenceAndValueMutantsVerifies(t *testing.T) {
	t.Parallel()

	result := runSuggest(t, suggestRequest(t, copyFixture(t, suggestCollapseCarrierFixture)))

	suggestion := collapsedShortNameSuggestion(t, result)
	assertCollapseSpansAbsenceAndValue(t, result, suggestion)
	assertEveryListedMutantHasALeg(t, result, suggestion)
	assertIsolatedLegCountMatches(t, result)
}

// collapsedShortNameSuggestion returns the fixture's one suggestion, which
// the collapse folded every short_name survivor into, verified.
func collapsedShortNameSuggestion(t *testing.T, result report.Report) report.Suggestion {
	t.Helper()

	if len(result.Suggestions) != 1 {
		t.Fatalf("want the one collapsed suggestion, got %s", statusSummary(result.Suggestions))
	}

	suggestion := result.Suggestions[0]
	if !strings.Contains(suggestion.Expression, "short_name") {
		t.Fatalf("expression = %q, want the short_name assertion", suggestion.Expression)
	}

	if suggestion.Status != report.SuggestionVerified {
		t.Fatalf("suggestion is %s (%s); the carrier deletes what the assertion reads, "+
			"and every other listed mutant fails it plainly",
			suggestion.Status, suggestion.StatusReason)
	}

	if suggestion.Verification == nil {
		t.Fatal("the verified suggestion carries no evidence")
	}

	return suggestion
}

// assertCollapseSpansAbsenceAndValue requires the suggestion's listed mutants
// to span the report's absence carriers and value members: the mix whose
// carrier alone used to refute the whole suggestion.
func assertCollapseSpansAbsenceAndValue(
	t *testing.T, result report.Report, suggestion report.Suggestion,
) {
	t.Helper()

	operators := mutantOperators(result)
	listed := append([]string{suggestion.MutantID}, suggestion.AlsoKills...)

	sawAbsence, sawValue := false, false
	for _, id := range listed {
		operator := operators[id]
		if operator == "COLL-DROP-ENTRY" || operator == "COLL-EMPTY" ||
			operator == "COLL-DROP-LAST" {
			sawAbsence = true
		}

		if strings.HasPrefix(operator, "STR-") {
			sawValue = true
		}
	}

	if !sawAbsence || !sawValue {
		t.Fatalf("the collapse lists %v; want an absence operator and a value operator "+
			"in one suggestion", listed)
	}
}

// assertEveryListedMutantHasALeg requires the mutant leg's evidence to name
// an outcome for every mutant the suggestion claims.
func assertEveryListedMutantHasALeg(
	t *testing.T, result report.Report, suggestion report.Suggestion,
) {
	t.Helper()

	operators := mutantOperators(result)
	detail := suggestion.Verification.Mutant.Detail

	for _, id := range append([]string{suggestion.MutantID}, suggestion.AlsoKills...) {
		if !strings.Contains(detail, id) {
			t.Fatalf("the mutant leg names no outcome for %s (%s): %q", id, operators[id], detail)
		}
	}
}

// assertIsolatedLegCountMatches requires the cost statement to count every
// leg verification ran: one per listed mutant of every suggestion whose
// isolated leg ran.
func assertIsolatedLegCountMatches(t *testing.T, result report.Report) {
	t.Helper()

	wantIsolated := 0
	for _, suggestion := range result.Suggestions {
		if suggestion.Verification == nil || len(suggestion.Verification.Mutant.Runs) == 0 {
			continue
		}

		wantIsolated += 1 + len(suggestion.AlsoKills)
	}

	found := false
	for _, warning := range result.Warnings {
		var full, isolated int
		if _, err := fmt.Sscanf(warning,
			"verification executed %d full-suite run(s) plus %d isolated mutant run(s)",
			&full, &isolated); err != nil {
			continue
		}

		found = true

		if full < 1 {
			t.Fatalf("the cost statement claims no full-suite run: %q", warning)
		}

		if isolated != wantIsolated {
			t.Fatalf("the cost statement counts %d isolated run(s), want one per listed "+
				"mutant (%d): %q", isolated, wantIsolated, warning)
		}
	}

	if !found {
		t.Fatalf("the report states no verification cost: %v", result.Warnings)
	}
}

// mutantOperators indexes the population's operator per mutant identifier.
func mutantOperators(result report.Report) map[string]string {
	operators := map[string]string{}
	for _, mutant := range result.Mutants {
		operators[mutant.ID] = mutant.Operator
	}

	return operators
}

func loadPublishedSchema(t *testing.T) map[string]any {
	t.Helper()

	content, err := os.ReadFile("../../docs/schema/report-2.15.0.json")
	if err != nil {
		t.Fatalf("reading the published schema: %v", err)
	}

	schema := map[string]any{}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decoding the published schema: %v", err)
	}

	return schema
}
