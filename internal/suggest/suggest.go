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

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// nullMarker is the spelling Terraform gives a value that is null, in test
// payloads and in assertion expressions alike.
const nullMarker = "null"

// Defect is a deliberately wrong assertion the generator can be made to emit.
//
// It exists for the suggestion-soundness gate and for nothing else: a
// verification contract nobody has seen reject a bad suggestion is a contract
// nobody has tested. Each defect is aimed at exactly one leg.
type Defect string

// Seeded defects exercise both verification legs and batch attribution.
const (
	// DefectNone is the ordinary generator.
	DefectNone Defect = ""
	// DefectWrongValue compares against a value the baseline does not have, so
	// the full-suite leg must reject it.
	DefectWrongValue Defect = "wrong-value"
	// DefectVacuous is always true, so it passes the full-suite leg and the
	// mutant must survive it — which only the isolated leg can see.
	DefectVacuous Defect = "vacuous"
	// DefectVanishing references an output the baseline does not declare, so
	// the run carrying it errors rather than fails, and Terraform's diagnostic
	// reports its own error instead of the assertion's message — a batch
	// failure that names no suggestion, which only splitting the batch
	// attributes.
	DefectVanishing Defect = "vanishing"
)

// SeededWrongValue is the value the wrong-value defect compares against.
const SeededWrongValue = "tf-mut-seeded-wrong-value"

// SeededVanishingOutput is the undeclared output the vanishing defect reads.
const SeededVanishingOutput = "tf_mut_seeded_missing"

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
	// Schemas supplies provider type evidence for resource and data-source paths.
	Schemas tfexec.Schemas
	// Baseline supplies each run's published output types; provider schemas do
	// not describe outputs.
	Baseline []fingerprint.Payload
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

	if g.Defect == DefectVanishing {
		expression = "output." + SeededVanishingOutput + ` == "` + SeededWrongValue + `"`
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
//
// An output record's metadata paths — its type descriptor, its sensitivity and
// unknown markers — are walked last: they change whenever the value does, they
// are never assertable, and otherwise the refusal they produce outranks the
// value's own in the report.
func (g Generator) suggestFor(
	survivor Survivor,
	target discovery.RunBlock,
	changes []fingerprint.Change,
) generated {
	renderer := render{schemas: g.Schemas}

	var value, metadata []fingerprint.Change
	carried := []fingerprint.Change{}
	for _, change := range changes {
		if change.Run != runKey(target) {
			continue
		}

		carried = append(carried, change)

		if fingerprint.IsOutputMetadata(change.Path) {
			metadata = append(metadata, change)

			continue
		}

		value = append(value, change)
	}

	evidence := NewEvidence(carried)
	for _, payload := range g.Baseline {
		if payload.Key() == runKey(target) {
			evidence.OutputTypes = payload.OutputTypes
			break
		}
	}

	for _, pass := range [][]fingerprint.Change{value, metadata} {
		walked := walkChanges(renderer, evidence, pass)
		if walked.admitted {
			return build(survivor, target, walked.expression)
		}

		first := walked.first
		if first == nil {
			first = fmt.Errorf("%w: the delta carries no change in the target run", ErrUnaddressable)
		}

		if walked.sensitive != nil {
			first = walked.sensitive
		}

		if len(pass) > 0 {
			skip := Skipped(survivor.id, target.Rel, target.Name, reasonOf(first), first.Error())

			return generated{skip: &skip}
		}
	}

	skip := Skipped(survivor.id, target.Rel, target.Name, SkipUnaddressable,
		"the delta carries no change in the target run")

	return generated{skip: &skip}
}

// walked is one pass of walkChanges: the expression a change admitted, the
// first refusal and the first sensitivity refusal out of the ones it did not,
// and whether anything was admitted.
type walked struct {
	expression string
	first      error
	sensitive  error
	admitted   bool
}

// walkChanges renders the first change a renderer admits, and carries the
// first refusal and the first sensitivity refusal out of the ones it does not.
func walkChanges(
	renderer render,
	evidence Evidence,
	changes []fingerprint.Change,
) walked {
	result := walked{}

	for _, change := range changes {
		rendered, err := expressChange(renderer, evidence, change)
		if err != nil {
			if result.first == nil {
				result.first = err
			}

			// A sensitivity refusal outranks every other refusal for the
			// reported status: when nothing is expressible and part of the
			// delta is sensitive, "the value appears in no artefact" is the
			// contract the reader has to know about, not the addressing
			// detail of some other change.
			if result.sensitive == nil && errors.Is(err, ErrSensitive) {
				result.sensitive = err
			}

			continue
		}

		result.expression, result.admitted = rendered, true

		return result
	}

	return result
}

// Express maps one delta change onto the assertion condition that would catch
// it, or returns the adapter refusal that stopped it.
//
// It is the whole three-adapter sweep behind one call, and it is exported
// because the fail-closed matrices are contracts about payload paths and
// provider types rather than about any one module: driving the real binary
// into producing each of the payload shapes on demand is not possible, exactly
// as it is not for the payload shapes `internal/fingerprint` is tested on.
func Express(
	run discovery.RunBlock,
	schemas tfexec.Schemas,
	evidence Evidence,
	change fingerprint.Change,
) (string, error) {
	_ = run // the adapter reads the module path from the address itself; see traversal.

	return expressChange(render{schemas: schemas}, evidence, change)
}

// Evidence carries what a whole delta proves about one change's neighbourhood.
//
// A single change says what a path held on each side; only the delta around it
// says what the value's shape was. The unknown markers say which parts of a
// plan value the baseline could not know — a length over such a value is a
// lie. The non-null proofs are the paths whose vanished members carried
// values, so a mutant that nulls a container is answered by an inequality and
// not by an index into it.
type Evidence struct {
	// UnknownMarkers are the delta's marker paths, canonical and complete.
	UnknownMarkers []string
	// ProvenNonNull records the paths whose baseline value the delta proves
	// was neither absent nor null: another change under the same path carried
	// a value.
	ProvenNonNull map[string]bool
	// InstanceCounts records, per resource address, the instance count the
	// delta proves the baseline had: a change whose value side is empty under
	// `resource[N]` proves instance N existed on the baseline.
	InstanceCounts map[string]int
	// OutputTypes supplies baseline output type evidence; absent evidence must
	// not be inferred from the payload's numeric indices.
	OutputTypes map[string]cty.Type
}

// NewEvidence reads a delta into the evidence its changes prove. It is the
// generator's own construction, and the matrices build it by hand.
func NewEvidence(changes []fingerprint.Change) Evidence {
	evidence := Evidence{
		ProvenNonNull:  map[string]bool{},
		InstanceCounts: map[string]int{},
	}

	for _, change := range changes {
		evidence.absorb(change, changes)
	}

	return evidence
}

// absorb folds one change into the accumulating evidence. The full change set
// travels with it: a nulled container only proves non-null where a peer change
// shows a value that survived underneath it.
func (e *Evidence) absorb(change fingerprint.Change, changes []fingerprint.Change) {
	if fingerprint.IsUnknownMarker(change.Path) {
		e.UnknownMarkers = append(e.UnknownMarkers, change.Path)
	}

	if change.Mutant == nullMarker && !e.ProvenNonNull[change.Path] {
		e.proveNonNull(change, changes)
	}

	e.countInstance(change, changes)
}

// proveNonNull marks a nulled container non-null where any peer change carries
// a real baseline value on the container or inside it: the mutation's nulling
// is provable rather than an absence the baseline shared.
func (e *Evidence) proveNonNull(change fingerprint.Change, changes []fingerprint.Change) {
	address, attribute, split := fingerprint.Split(change.Path)
	container := split && !strings.HasPrefix(address, "output.") && hclsyntax.ValidIdentifier(attribute)

	for _, other := range changes {
		if other.Run != change.Run || other.Baseline == "" || other.Baseline == nullMarker {
			continue
		}

		if other.Path == change.Path ||
			(container && (strings.HasPrefix(other.Path, change.Path+".") ||
				strings.HasPrefix(other.Path, change.Path+"["))) {
			e.ProvenNonNull[change.Path] = true

			break
		}
	}
}

// countInstance folds a vanished instance's change into the resource count
// the count rendering is judged against. An instance counts as vanished only
// where the delta also carries a change at the instance's root record — its
// address or index: a value-body change alone cannot tell a vanished instance
// from a vanished value.
func (e *Evidence) countInstance(change fingerprint.Change, changes []fingerprint.Change) {
	if change.Mutant != "" {
		return
	}

	resource, index, counted := fingerprint.CountedInstance(change.Path)
	if !counted || !instanceRecorded(change.Path, changes) {
		return
	}

	if index+1 > e.InstanceCounts[resource] {
		e.InstanceCounts[resource] = index + 1
	}
}

// instanceRecorded reports whether the change set carries a change at an
// instance's root record: the address or index Terraform writes for the
// instance itself, whose mutant side vanished with it.
func instanceRecorded(path string, changes []fingerprint.Change) bool {
	root := fingerprint.CountedInstanceRoot(path)

	for _, change := range changes {
		if change.Mutant != "" {
			continue
		}

		if change.Path == root+".address" || change.Path == root+".index" {
			return true
		}
	}

	return false
}

// markersUnder reports whether the delta's markers reach the subtree a value
// path names.
func (e *Evidence) markersUnder(valuePath string) bool {
	for _, marker := range e.UnknownMarkers {
		base, found := fingerprint.UnknownValueBase(marker)
		if !found {
			continue
		}

		if base == valuePath || strings.HasPrefix(base, valuePath+".") ||
			strings.HasPrefix(base, valuePath+"[") {
			return true
		}
	}

	return false
}

// expressChange runs one change through the three adapters in order.
// Sensitivity comes first: a sensitive value must not reach a renderer at all.
func expressChange(renderer render, evidence Evidence, change fingerprint.Change) (string, error) {
	if change.Sensitive {
		return "", fmt.Errorf("%w: Terraform marks the value at this path, or a container "+
			"of it, sensitive", ErrSensitive)
	}

	parts, err := traversal(change.Path)
	if err != nil {
		return "", err
	}

	return renderer.equality(parts, change.Baseline, change.Mutant, evidence)
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

// SuggestionIDFromMessage recovers the suggestion identifier a failed
// assertion names: the inverse of VerifiedMessage. Terraform copies a failed
// assertion's error_message verbatim into the failing run's diagnostic, so a
// batch run's diagnostics name the suggestions that failed in it, and
// verification can attribute a failing batch to those suggestions alone. The
// second result is false for any other text.
func SuggestionIDFromMessage(message string) (string, bool) {
	rest, found := strings.CutPrefix(message, "tf-mut suggestion ")
	if !found {
		return "", false
	}

	id, rest, found := strings.Cut(rest, " catches mutant ")
	if !found || id == "" || rest == "" {
		return "", false
	}

	return id, true
}
