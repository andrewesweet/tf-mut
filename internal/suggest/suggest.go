// Package suggest turns a proven survivor delta into the assertion that would
// have killed it.
//
// The engine grades; this is what turns grading into improvement. Every
// suggestion is generated behind three fail-closed adapters — addressing,
// rendering and sensitivity — and each adapter has its own honest outcome, so
// what the generator cannot express is reported as a limit and never as a
// refutation. Nothing here writes anything: generation produces a candidate
// and a patch, verification decides whether the candidate is true, and
// applying is a separate protocol bound to the bytes that were verified. The
// lifecycle those outcomes move through — candidate, verified, refuted,
// skipped — is owned by this package's types, and the application layer
// projects them onto the published report at the boundary.
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
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

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

// Survivor is a proven survivor the generator is asked about: the mutant
// identifier and the masked delta the oracle proved against the baseline. The
// engine grades; this value is what grading hands over — the delta, and
// nothing about how it was diagnosed.
type Survivor struct {
	id    string
	delta []fingerprint.Change
}

// NewSurvivor names a survivor and its delta. The delta is copied, so the
// value is immutable once built.
func NewSurvivor(id string, delta []fingerprint.Change) Survivor {
	return Survivor{id: id, delta: slices.Clone(delta)}
}

// Generator produces suggestions from the selected survivors.
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
// honest to say about, in survivor order: the candidates awaiting
// verification, and the skipped suggestions whose reason an adapter chose.
func (g Generator) Generate(survivors []Survivor) ([]Candidate, []Suggestion) {
	candidates := []Candidate{}
	skipped := []Suggestion{}
	seeded := false

	for _, survivor := range survivors {
		out := g.generateOne(survivor)

		if out.skip != nil {
			skipped = append(skipped, *out.skip)

			continue
		}

		if out.candidate == nil {
			continue
		}

		// The seed lands on the first candidate only. That is what makes the
		// gate prove attribution as well as rejection: the rest of the batch
		// is real, so a batch-wide kill check would have hidden the defect and
		// the isolated check cannot.
		if !seeded && g.Defect != DefectNone {
			seeded = true
			out = g.seed(survivor, *out.candidate)

			if out.skip != nil {
				skipped = append(skipped, *out.skip)

				continue
			}
		}

		candidates = append(candidates, *out.candidate)
	}

	return collapse(candidates), skipped
}

// generated is one survivor's generation outcome: a candidate awaiting
// verification, a skipped suggestion with its reason, or nothing — the
// generator has nothing honest to say about that survivor.
type generated struct {
	candidate *Candidate
	skip      *Suggestion
}

// collapse folds candidates that share a target run and an expression into
// one suggestion (round-3 review, PR #69): five survivors one assertion kills
// are one review item and one write, not five byte-identical assert blocks.
// The first candidate keeps the identity; the rest become its AlsoKills, and
// the isolated verification leg still runs once per listed mutant, so
// attribution stays per-mutant.
func collapse(candidates []Candidate) []Candidate {
	collapsed := []Candidate{}
	carrier := map[string]int{}

	for _, candidate := range candidates {
		key := candidate.TargetFile() + "\x00" + candidate.TargetRun() + "\x00" + candidate.Expression()

		if index, found := carrier[key]; found {
			collapsed[index].alsoKills = append(collapsed[index].alsoKills, candidate.MutantID())

			continue
		}

		carrier[key] = len(collapsed)
		collapsed = append(collapsed, candidate)
	}

	return collapsed
}

// seed rewrites a candidate into the named deliberately wrong assertion.
func (g Generator) seed(survivor Survivor, candidate Candidate) generated {
	target, found := g.placement(survivor.delta)
	if !found {
		return generated{candidate: &candidate}
	}

	// Terraform refuses a constant condition ("must refer to at least one
	// object"), so the vacuous defect is a tautology over the real reference:
	// always true, and only the isolated mutant leg can see it kills nothing.
	reference := strings.SplitN(candidate.Expression(), " == ", partsOfAnEquality)[0]

	expression := reference + " == " + reference
	if g.Defect == DefectWrongValue {
		expression = reference + ` == "` + SeededWrongValue + `"`
	}

	return build(survivor, target, expression)
}

// partsOfAnEquality is the operand count of the equality the generator writes.
const partsOfAnEquality = 2

// generateOne produces the suggestion for one survivor.
func (g Generator) generateOne(survivor Survivor) generated {
	delta := survivor.delta
	if len(delta) == 0 {
		return generated{}
	}

	target, found := g.placement(delta)
	if !found {
		return generated{}
	}

	// The JSON test writer is deliberately not built, so a survivor carried by
	// a JSON run has no patch and `--apply` never touches its file.
	if target.JSONDeclared {
		skip := Skipped(survivor.id, target.Rel, target.Name, SkipUnsupportedTarget,
			"the target run is declared in "+target.Rel+
				", and no JSON test writer is built: this suggestion is reported and never applied")

		return generated{skip: &skip}
	}

	return g.suggestFor(survivor, target, delta)
}

// placement chooses the run the assertion is written into: the run whose
// fingerprint carried the delta, and where several did, the first in
// declaration order.
func (g Generator) placement(changes []fingerprint.Change) (discovery.RunBlock, bool) {
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
	survivor Survivor,
	target discovery.RunBlock,
	changes []fingerprint.Change,
) generated {
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

		return build(survivor, target, expression)
	}

	if sensitive != nil {
		first = sensitive
	}

	if first == nil {
		first = fmt.Errorf("%w: the delta carries no change in the target run", ErrUnaddressable)
	}

	skip := Skipped(survivor.id, target.Rel, target.Name, reasonOf(first), first.Error())

	return generated{skip: &skip}
}

// Express maps one delta change onto the assertion condition that would catch
// it, or returns the adapter refusal that stopped it.
//
// It is the whole three-adapter sweep behind one call, and it is exported
// because the fail-closed matrices are contracts about payload paths and
// provider types rather than about any one module: driving the real binary
// into producing each of the fifteen shapes on demand is not possible, exactly
// as it is not for the payload shapes `internal/fingerprint` is tested on.
func Express(
	run discovery.RunBlock,
	schemas tfexec.Schemas,
	change fingerprint.Change,
) (string, error) {
	_ = run // the adapter reads the module path from the address itself; see traversal.

	return expressChange(render{schemas: schemas}, change)
}

// expressChange runs one change through the three adapters in order.
// Sensitivity comes first: a sensitive value must not reach a renderer at all.
func expressChange(renderer render, change fingerprint.Change) (string, error) {
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

// reasonOf maps an adapter's refusal onto its own outcome reason.
func reasonOf(err error) SkipReason {
	switch {
	case errors.Is(err, ErrSensitive):
		return SkipSensitive
	case errors.Is(err, ErrUnaddressable):
		return SkipUnaddressable
	default:
		return SkipUnrenderable
	}
}

// build renders the candidate for an admitted change.
//
// The patch is rendered with the same message renderer verification and apply
// use, after the stable identifier is known: the bytes a reporter shows, the
// bytes the sandbox verifies and the bytes apply writes must be one sequence,
// or the digest protocol proves a file nobody was shown. Where the target
// cannot be rewritten the outcome is a skip — a candidate with no patch has no
// spelling.
func build(survivor Survivor, target discovery.RunBlock, expression string) generated {
	id := identifier(survivor.id, target.Rel, target.Name, expression)

	patch, err := PatchFor(target, expression, VerifiedMessage(id, survivor.id))
	if err != nil {
		skip := Skipped(survivor.id, target.Rel, target.Name, SkipUnaddressable,
			"the target run could not be rewritten: "+err.Error())

		return generated{skip: &skip}
	}

	candidate := NewCandidate(survivor.id, target.Rel, target.Name, expression, patch)

	return generated{candidate: &candidate}
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
