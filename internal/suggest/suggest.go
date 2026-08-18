// Package suggest turns a proven survivor delta into the assertion that would
// have killed it.
//
// The engine grades; this is what turns grading into improvement. Every
// suggestion is generated behind three fail-closed adapters — addressing,
// rendering and sensitivity — and each adapter has its own honest outcome, so
// what the generator cannot express is reported as a limit and never as a
// refutation. Nothing here writes anything: generation produces a candidate and
// a patch, verification decides whether the candidate is true, and applying is
// a separate protocol bound to the bytes that were verified.
package suggest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// The diagnoses a suggestion is generated for.
//
// Each one names a proven, expressible delta. The indeterminate diagnoses name
// a comparison the oracle could not make, so there is nothing to assert; and
// `StructurallyUnassertable` is not a survivor diagnosis at all — its skeleton
// generation was removed from this milestone and relocated behind the
// minable-share measurement it always belonged to.
//
//nolint:gochecknoglobals // an immutable lookup table.
var suggestibleDiagnoses = []report.Diagnosis{
	report.NoAssertion, report.WeakAssertion, report.Unasserted,
}

// Defect is a deliberately wrong assertion the generator can be made to emit.
//
// It exists for the suggestion-soundness gate and for nothing else: a
// verification contract nobody has seen reject a bad suggestion is a contract
// nobody has tested. Each defect is aimed at exactly one leg.
type Defect string

// The two seeded defects the soundness gate requires.
const (
	// DefectNone is the ordinary generator.
	DefectNone Defect = ""
	// DefectWrongValue compares against a value the baseline does not have, so
	// the full-suite leg must reject it.
	DefectWrongValue Defect = "wrong-value"
	// DefectVacuous is always true, so it passes the full-suite leg and the
	// mutant must survive it — which only the isolated leg can see.
	DefectVacuous Defect = "vacuous"
)

// SeededWrongValue is the value the wrong-value defect compares against.
const SeededWrongValue = "tf-mut-seeded-wrong-value"

// Generator produces suggestions from a completed report.
type Generator struct {
	// Configuration is the discovered module under test.
	Configuration discovery.Configuration
	// Schemas is the rendering contract's type source.
	Schemas tfexec.Schemas
	// Defect seeds a wrong assertion into the first candidate the generator
	// produces. It is a seam control for the soundness gate, not a flag.
	Defect Defect
}

// Generate returns one suggestion per survivor the generator has something
// honest to say about, in report order.
func (g Generator) Generate(mutants []report.Mutant) []report.Suggestion {
	suggestions := []report.Suggestion{}
	seeded := false

	for _, mutant := range mutants {
		suggestion, ok := g.generateOne(mutant)
		if !ok {
			continue
		}

		// The seed lands on the first candidate only. That is what makes the
		// gate prove attribution as well as rejection: the rest of the batch
		// is real, so a batch-wide kill check would have hidden the defect and
		// the isolated check cannot.
		if !seeded && g.Defect != DefectNone && suggestion.Status == report.SuggestionCandidate {
			seeded = true
			suggestion = g.seed(mutant, suggestion)
		}

		suggestions = append(suggestions, suggestion)
	}

	return suggestions
}

// seed rewrites a candidate into the named deliberately wrong assertion.
func (g Generator) seed(mutant report.Mutant, suggestion report.Suggestion) report.Suggestion {
	target, found := g.placement(mutant.Verdict.Evidence.Delta)
	if !found {
		return suggestion
	}

	// Terraform refuses a constant condition ("must refer to at least one
	// object"), so the vacuous defect is a tautology over the real reference:
	// always true, and only the isolated mutant leg can see it kills nothing.
	reference := strings.SplitN(suggestion.Expression, " == ", partsOfAnEquality)[0]

	expression := reference + " == " + reference
	if g.Defect == DefectWrongValue {
		expression = reference + ` == "` + SeededWrongValue + `"`
	}

	return candidate(mutant, target, expression)
}

// partsOfAnEquality is the operand count of the equality the generator writes.
const partsOfAnEquality = 2

// generateOne produces the suggestion for one survivor.
func (g Generator) generateOne(mutant report.Mutant) (report.Suggestion, bool) {
	if mutant.State != report.Survived || mutant.Verdict == nil {
		return report.Suggestion{}, false
	}

	if !slices.Contains(suggestibleDiagnoses, mutant.Verdict.Diagnosis) {
		return report.Suggestion{}, false
	}

	changes := mutant.Verdict.Evidence.Delta
	if len(changes) == 0 {
		return report.Suggestion{}, false
	}

	target, found := g.placement(changes)
	if !found {
		return report.Suggestion{}, false
	}

	// The JSON test writer is deliberately not built, so a survivor carried by
	// a JSON run has no patch and `--apply` never touches its file.
	if target.JSONDeclared {
		return skipped(mutant, target, report.SuggestionSkippedUnsupportedTarget,
			"the target run is declared in "+target.Rel+
				", and no JSON test writer is built: this suggestion is reported and never applied"), true
	}

	return g.suggestFor(mutant, target, changes), true
}

// placement chooses the run the assertion is written into: the run whose
// fingerprint carried the delta, and where several did, the first in
// declaration order.
func (g Generator) placement(changes []report.Change) (discovery.RunBlock, bool) {
	carrying := map[string]bool{}
	for _, change := range changes {
		carrying[change.Run] = true
	}

	for _, run := range g.Configuration.Tests.Runs {
		if carrying[runKey(run)] {
			return run, true
		}
	}

	return discovery.RunBlock{}, false //nolint:exhaustruct // no run carried the delta.
}

// runKey is the payload key of a run block: the module-relative test file and
// the run name, which is how a delta names the run that carried it.
func runKey(run discovery.RunBlock) string {
	return run.Rel + "::" + run.Name
}

// suggestFor walks the delta for the first change all three adapters admit,
// and reports the first change's own refusal where none is admitted.
func (g Generator) suggestFor(
	mutant report.Mutant,
	target discovery.RunBlock,
	changes []report.Change,
) report.Suggestion {
	renderer := render{schemas: g.Schemas}

	var first, sensitive error

	for _, change := range changes {
		if change.Run != runKey(target) {
			continue
		}

		expression, err := expressChange(renderer, change)
		if err != nil {
			if first == nil {
				first = err
			}

			// A sensitivity refusal outranks every other refusal for the
			// reported status: when nothing is expressible and part of the
			// delta is sensitive, "the value appears in no artefact" is the
			// contract the reader has to know about, not the addressing
			// detail of some other change.
			if sensitive == nil && errors.Is(err, ErrSensitive) {
				sensitive = err
			}

			continue
		}

		return candidate(mutant, target, expression)
	}

	if sensitive != nil {
		first = sensitive
	}

	if first == nil {
		first = fmt.Errorf("%w: the delta carries no change in the target run", ErrUnaddressable)
	}

	return skipped(mutant, target, statusOf(first), first.Error())
}

// Express maps one delta change onto the assertion condition that would catch
// it, or returns the adapter refusal that stopped it.
//
// It is the whole three-adapter sweep behind one call, and it is exported
// because the fail-closed matrices are contracts about payload paths and
// provider types rather than about any one module: driving the real binary into
// producing each of the fifteen shapes on demand is not possible, exactly as it
// is not for the payload shapes `internal/fingerprint` is tested on.
func Express(
	run discovery.RunBlock,
	schemas tfexec.Schemas,
	change report.Change,
) (string, error) {
	_ = run // the adapter reads the module path from the address itself; see traversal.

	return expressChange(render{schemas: schemas}, change)
}

// expressChange runs one change through the three adapters in order.
// Sensitivity comes first: a sensitive value must not reach a renderer at all.
func expressChange(renderer render, change report.Change) (string, error) {
	if change.Sensitive {
		return "", fmt.Errorf("%w: Terraform marks the value at this path, or a container "+
			"of it, sensitive", ErrSensitive)
	}

	parts, err := traversal(change.Path)
	if err != nil {
		return "", err
	}

	return renderer.equality(parts, change.Baseline)
}

// ErrSensitive reports a delta whose value Terraform marks sensitive.
//
// The sensitivity metadata is retained by the fingerprint because `issensitive`
// is assertable; retaining it is not permission to render the value. A
// suggestion refused here carries the value in no artefact: no expression, no
// patch, no error message, and nothing any reporter renders.
var ErrSensitive = errors.New("the delta's value is sensitive")

// statusOf maps an adapter's refusal onto its own outcome status.
func statusOf(err error) report.SuggestionStatus {
	switch {
	case errors.Is(err, ErrSensitive):
		return report.SuggestionSkippedSensitive
	case errors.Is(err, ErrUnaddressable):
		return report.SuggestionSkippedUnaddressable
	default:
		return report.SuggestionSkippedUnrenderable
	}
}

// candidate builds the suggestion for an admitted change.
//
// The patch is rendered with the same message renderer verification and apply
// use, after the stable identifier is known: the bytes a reporter shows, the
// bytes the sandbox verifies and the bytes apply writes must be one sequence,
// or the digest protocol proves a file nobody was shown.
func candidate(
	mutant report.Mutant,
	target discovery.RunBlock,
	expression string,
) report.Suggestion {
	id := identifier(mutant.ID, target.Rel, target.Name, expression)

	suggestion := report.Suggestion{
		ID:             id,
		MutantID:       mutant.ID,
		TargetFile:     target.Rel,
		TargetRun:      target.Name,
		Status:         report.SuggestionCandidate,
		Expression:     expression,
		Patch:          "",
		VerifiedDigest: "",
		Verification:   nil,
		StatusReason:   "",
	}

	patch, err := PatchFor(target, expression, VerifiedMessage(id, mutant.ID))
	if err != nil {
		return skipped(mutant, target, report.SuggestionSkippedUnaddressable,
			"the target run could not be rewritten: "+err.Error())
	}

	suggestion.Patch = patch

	return suggestion
}

// skipped builds a suggestion that carries a status and a reason and no patch.
func skipped(
	mutant report.Mutant,
	target discovery.RunBlock,
	status report.SuggestionStatus,
	reason string,
) report.Suggestion {
	return report.Suggestion{
		ID:             identifier(mutant.ID, target.Rel, target.Name, string(status)),
		MutantID:       mutant.ID,
		TargetFile:     target.Rel,
		TargetRun:      target.Name,
		Status:         status,
		Expression:     "",
		Patch:          "",
		VerifiedDigest: "",
		Verification:   nil,
		StatusReason:   reason,
	}
}

// identifier is the stable suggestion ID: a content hash over the mutant
// identifier, the target run and the expression. It survives a re-run and an
// unrelated edit, which is what makes `--apply` selectable by identifier.
func identifier(mutantID, file, run, expression string) string {
	digest := sha256.Sum256([]byte(mutantID + "\x00" + file + "\x00" + run + "\x00" + expression))

	return hex.EncodeToString(digest[:])[:identifierLength]
}

// identifierLength matches the mutant identifier's width.
const identifierLength = 12

// Digest is the SHA-256 of a file's current bytes, which is what binds an
// apply to the bytes verification proved.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)

	return hex.EncodeToString(sum[:])
}

// TargetPath resolves a suggestion's module-relative target file.
func TargetPath(moduleDir, relative string) string {
	return filepath.Join(moduleDir, filepath.FromSlash(relative))
}

// ReadTarget reads a suggestion's target file.
func ReadTarget(moduleDir, relative string) ([]byte, error) {
	content, err := os.ReadFile(TargetPath(moduleDir, relative))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", relative, err)
	}

	return content, nil
}

// VerifiedMessage is the `error_message` a verified-and-applied assertion
// carries. Verification and apply must write byte-identical assertions — the
// digest protocol proves the file, and this shared renderer is what keeps the
// assertion itself from drifting between the two.
func VerifiedMessage(suggestionID, mutantID string) string {
	return "tf-mut suggestion " + suggestionID + " catches mutant " + mutantID
}

// Statuses renders a suggestion set as its status counts, in vocabulary order,
// for the summaries a reporter prints.
func Statuses(suggestions []report.Suggestion) string {
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
