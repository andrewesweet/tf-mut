package suggest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// ErrUnrenderable reports a value or path the generator cannot express
// type-correctly in a Terraform assertion.
//
// The installed `hclwrite.TokensForValue` renders lists, sets and tuples with
// the same `[...]` syntax and maps and objects with the same `{...}` syntax,
// and Terraform v1.15.8 confirms the consequence: `toset(["a"]) == ["a"]` is
// false. Generation therefore refuses ambiguous whole-value equalities and
// uses provider or baseline output types to justify element paths. Unsupported
// shapes are reported as a limit rather than generated and refuted.
var ErrUnrenderable = errors.New("no type-correct Terraform equality renders this value")

// The two canonical renderings that carry no leaf.
const (
	emptyObject = "{}"
	emptyArray  = "[]"
)

// render is the value-rendering contract.
type render struct {
	// schemas supplies provider type evidence. The payload alone cannot
	// distinguish a set from a list, so where a provider types the attribute
	// its type decides, and where nothing types it beyond the scalar in front
	// of us the value is scalar-rendered.
	schemas tfexec.Schemas
}

// equality renders the assert condition for one delta change, or fails closed.
func (r render) equality(
	parts traversalParts,
	change fingerprint.Change,
	evidence Evidence,
) (string, error) {
	baseline, mutant := change.Baseline, change.Mutant
	expression, resource, attribute := parts.expression, parts.resource, parts.attribute

	// A path that continues past the value body — an element of a list-valued
	// output, a nested block, a keyed collection — is spelled by the walk, one
	// schema- or grammar-justified segment at a time. Every form below uses
	// that spelling; the provisional dotted join the address adapter emitted
	// is never asserted on its own.
	continued := strings.HasPrefix(attribute, "[") || len(parts.source.Segments()) > 1 ||
		(strings.HasPrefix(resource, "output.") && attribute != "")

	normalize := func(need terminal) (string, error) {
		if !continued {
			return expression, nil
		}

		return r.spellContinuation(parts.source, resource, attribute, evidence.OutputTypes[resource], need)
	}

	// A mutant that nulls the value is answered by an inequality, whatever the
	// baseline held: it is true on the baseline and false under the mutant.
	// The baseline has to prove the value was there — present in the payload,
	// or proven non-null by the neighbourhood the delta carries — because a
	// wholly unknown value makes `!= null` an assertion about nothing.
	if mutant == nullMarker && (baseline != "" || evidence.ProvenNonNull[parts.path]) {
		spelled, err := normalize(terminalAny)
		if err != nil {
			return "", err
		}

		if attribute == "" {
			return spelled + " != null", nil
		}

		return tolerant(spelled) + " != null", nil
	}

	// A change whose value side is empty under a counted instance is a
	// vanishing instance — the mutant removed it, along with everything under
	// it — and the count equality is what that proves: the baseline had a
	// collection of instances, one more than the highest vanished index, and
	// `length` over the un-indexed resource is type-correct over exactly
	// that. Asserting the vanished instance's own values would be refused by
	// Terraform as an invalid index under the mutant, which is a run error
	// and no kill; the count is the assertion that kills.
	if counted, handled, err := countEquality(resource, mutant, evidence); handled {
		return counted, err
	}

	if baseline == "" {
		return "", fmt.Errorf("%w: the value is absent from the baseline, and an "+
			"assertion cannot express the absence of a value", ErrUnrenderable)
	}

	if change.BaselineVolatile {
		return "", fmt.Errorf("%w: the volatile mask removed what a later run "+
			"differs in, so no assertion can honestly pin it", ErrVolatile)
	}

	// A delta the mutant's own plan marks unknown: under a `command = plan`
	// run the value cannot be read by any assertion, so the equality would
	// error with "Unknown condition value" when the suite replays it against
	// the mutant — deleting a provider-computed attribute leaves it unknown at
	// plan, and the value is not there to compare until apply. The refusal is
	// reported as a skip that names the value; the same delta under apply
	// renders below unchanged.
	if evidence.Plan && mutant == "" && evidence.markersUnder(parts.path) {
		return "", fmt.Errorf("%w: the mutant's plan marks %s unknown at plan, and a "+
			"`command = plan` run can evaluate no equality over it: the suggestion would "+
			"error with %q when replayed", ErrUnrenderable, parts.path, "Unknown condition value")
	}

	// A size delta the baseline proves: the collection was empty. `length` is
	// type-correct over every collection, but never over a value whose parts
	// the baseline could not know: the plan marks those, and a length over
	// unknowns is a lie the baseline run exposes.
	if baseline == emptyObject || baseline == emptyArray {
		spelled, err := normalize(terminalCollection)
		if err != nil {
			return "", err
		}

		if evidence.markersUnder(parts.path) {
			return "", fmt.Errorf("%w: the plan marks parts of this value unknown, and a "+
				"length over unknowns is not evidence", ErrUnrenderable)
		}

		return tolerant("length("+spelled+")") + " == 0", nil
	}

	segments := parts.source.Segments()

	switch {
	case continued:
		spelled, err := normalize(terminalScalar)
		if err != nil {
			return "", err
		}

		return scalarEquality(tolerant(spelled), baseline)
	case attribute == "":
		// A whole output value. No provider schema types an output, so the
		// scalar in front of us is the only type evidence there is.
		return scalarEquality(expression, baseline)
	default:
		return r.leafEquality(expression, resource, segments[0], baseline)
	}
}

// tolerant renders the absence-tolerant form of an assertion reference: a
// reference becomes a try with a null default, so a missing key, attribute
// or instance fails the assertion instead of erroring the run.
func tolerant(reference string) string {
	return "try(" + reference + ", null)"
}

// countEquality renders the vanished-instance form: a resource whose mutant
// side lost a change proves instances vanished, and the delta-rooted count is
// a baseline-true, mutant-false length equality. The bool reports whether the
// branch applies at all; where it does, the error carries the refusal.
func countEquality(resource, mutant string, evidence Evidence) (string, bool, error) {
	if mutant != "" || evidence.InstanceCounts == nil {
		return "", false, nil
	}

	base, index, has := splitInstanceIndex(resource)
	if !has {
		return "", false, nil
	}

	count, counted := evidence.InstanceCounts[base]
	if !counted || index >= count {
		return "", false, nil
	}

	if err := parseTraversal(base); err != nil {
		return "", true, fmt.Errorf("%w: %s is not expressible as a dotted traversal",
			ErrUnrenderable, base)
	}

	return "length(" + base + ") == " + strconv.Itoa(count), true, nil
}

// spellContinuation spells a path that continues past the value body: a list-
// valued output's element, a nested block's attribute, or a keyed collection's
// entry. The walk starts from the resource address and consumes one path
// segment at a time, so every spelling it emits is one the segment in front of
// it justifies.
func (r render) spellContinuation(
	p fingerprint.Path,
	resource, attribute string,
	outputType cty.Type,
	need terminal,
) (string, error) {
	segments := p.Segments()

	kind, resourceType, ok := schemaCoordinates(resource)
	if !ok {
		// No provider schema types an output. The walk uses the baseline's
		// published output type to justify each step; where nothing types it,
		// the walk keeps the grammar floor the payload alone can justify.
		suffix, err := walkOutputPath(attribute, segments, outputType, need)
		if err != nil {
			return "", err
		}

		spelled := resource + suffix
		if err := parseTraversal(spelled); err != nil {
			return "", notDotted(spelled)
		}

		return spelled, nil
	}

	root, found := r.schemas.Block(kind, resourceType)
	if !found {
		return "", nestedValue(attribute)
	}

	body, err := r.walkBlockSteps(kind, resourceType, root, segments, need)
	if err != nil {
		return "", err
	}

	spelled := resource + body
	if err := parseTraversal(spelled); err != nil {
		return "", notDotted(spelled)
	}

	return spelled, nil
}

// walkBlockSteps renders a payload path's segments inside a block schema's
// namespace. A name resolves against the block's nested block types and
// attributes; the first attribute consumes the remainder of the path in type
// space, because inside a block an attribute is never a map key.
func (r render) walkBlockSteps(
	kind, resourceType string,
	block tfexec.SchemaBlock,
	steps []string,
	need terminal,
) (string, error) {
	suffix := &strings.Builder{}
	path := []string{}

	for i := 0; i < len(steps); i++ {
		step := steps[i]
		name, key, indexed := cutIndex(step)
		path = append(path, name)

		nested, isBlock := block.BlockTypes[name]
		if !isBlock {
			if _, found := block.Attributes[name]; !found {
				return "", nestedValue(strings.Join(steps, "."))
			}

			described, _ := r.schemas.Attribute(kind, resourceType, path)

			tail, err := walkAttributeSteps(described, name, key, indexed, steps[i+1:], need)
			if err != nil {
				return "", err
			}

			return suffix.String() + tail, nil
		}

		// A map-nested block's key travels as the next dotted segment —
		// `mounts.shared.source` — because the payload flattens every key
		// on its dots; an attached bracketed key is the same key written
		// inline. Every other nesting mode spells itself through the same
		// step.
		if nested.NestingMode == tfexec.NestingMap && !indexed {
			piece, inner, err := mapKeyStep(nested, name, steps, i)
			if err != nil {
				return "", err
			}

			suffix.WriteString(piece)
			block = inner
			i++

			continue
		}

		piece, inner, err := blockTypeStep(nested, name, key, indexed)
		if err != nil {
			return "", err
		}

		suffix.WriteString(piece)
		block = inner
	}

	return "", fmt.Errorf("%w: the path ends at a nested block, and no scalar "+
		"equality reads a block value", ErrUnrenderable)
}

// mapKeyStep renders a map-nested block's key step: the key is the next dotted
// segment, and the path must carry one.
func mapKeyStep(
	nested tfexec.SchemaBlockType,
	name string,
	steps []string,
	i int,
) (string, tfexec.SchemaBlock, error) {
	if i+1 >= len(steps) {
		return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s is map-nested, "+
			"and the payload path names no key for it", ErrUnrenderable, name)
	}

	return blockTypeStep(nested, name, steps[i+1], true)
}

// blockTypeStep renders one nested-block step under its nesting mode's rules:
// list-nested blocks index numerically, map-nested blocks take the payload's
// key, and a set is refused outright because no assertion can name one of its
// elements deterministically.
func blockTypeStep(
	blockType tfexec.SchemaBlockType,
	name, key string,
	indexed bool,
) (string, tfexec.SchemaBlock, error) {
	switch blockType.NestingMode {
	case tfexec.NestingList:
		if !indexed || !digitsOnly(key) {
			return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s is list-nested, "+
				"and %s does not index it", ErrUnrenderable, name, name+"["+key+"]")
		}

		return "." + name + "[" + key + "]", blockType.Block, nil
	case tfexec.NestingSingle, tfexec.NestingGroup:
		if indexed {
			return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s is %s-nested, "+
				"and cannot be indexed", ErrUnrenderable, name, blockType.NestingMode)
		}

		return "." + name, blockType.Block, nil
	case tfexec.NestingMap:
		if !indexed {
			return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s is map-nested, "+
				"and the payload path names no key for it", ErrUnrenderable, name)
		}

		return "." + name + "[" + quoteKey(key) + "]", blockType.Block, nil
	case tfexec.NestingSet:
		return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s is set-nested, and a "+
			"set has no index an assertion could name", ErrUnrenderable, name)
	default:
		return "", tfexec.SchemaBlock{}, fmt.Errorf("%w: the block %s has the unknown "+
			"nesting mode %q", ErrUnrenderable, name, blockType.NestingMode)
	}
}

// walkAttributeSteps renders the remainder of a path that has entered an
// attribute: the attribute is consumed, its element indexed if the path did,
// and everything after it is consumed in type space by the walk the type
// justifies. The leaf the form needs decides where the walk may stop.
func walkAttributeSteps(
	attribute tfexec.Attribute,
	name, key string,
	indexed bool,
	rest []string,
	need terminal,
) (string, error) {
	spelled := "." + name

	declared, known := attribute.ValueType()

	if !indexed {
		if len(rest) == 0 {
			if !known {
				return spelled, nil
			}

			return spelled, need.accepts(declared)
		}

		if !known {
			return "", fmt.Errorf("%w: no provider schema types %s, so nothing can tell the "+
				"rest of the path from a map key", ErrUnrenderable, name)
		}

		continued, err := walkTypeSteps(declared, rest, need)
		if err != nil {
			return "", err
		}

		return spelled + continued, nil
	}

	if !known {
		return "", fmt.Errorf("%w: no provider schema types %s, so %s cannot be told "+
			"apart from a set, which has no index", ErrUnrenderable, name, name+"["+key+"]")
	}

	element, err := indexType(declared, name, key)
	if err != nil {
		return "", err
	}

	spelled += "[" + key + "]"

	if len(rest) == 0 {
		return spelled, need.accepts(element)
	}

	continued, err := walkTypeSteps(element, rest, need)
	if err != nil {
		return "", err
	}

	return spelled + continued, nil
}

// terminal is the leaf shape a form needs from the walk: the scalar equality
// needs a primitive, the emptiness form anything `length` reads — a collection,
// or a string, whose length Terraform also takes — and the null comparison any
// value it can reach.
type terminal int

const (
	terminalScalar terminal = iota
	terminalCollection
	terminalAny
)

func (need terminal) accepts(declared cty.Type) error {
	switch need {
	case terminalScalar:
		if declared.IsPrimitiveType() {
			return nil
		}

		return fmt.Errorf("%w: the value is typed %s, which no scalar equality can "+
			"express", ErrUnrenderable, declared.FriendlyName())
	case terminalCollection:
		if declared == cty.Number || declared == cty.Bool {
			return fmt.Errorf("%w: the value is typed %s, and only a collection or a "+
				"string has a length to compare", ErrUnrenderable, declared.FriendlyName())
		}

		return nil
	case terminalAny:
		return nil
	default:
		return fmt.Errorf("%w: the leaf shape %d is one no equality form names",
			ErrUnrenderable, need)
	}
}

// stepReading is one reading a type justifies for the path's next segments:
// the traversal it adds, the type it leaves the walk holding, and how many
// payload segments it consumed.
type stepReading struct {
	spelling string
	declared cty.Type
	consumed int
}

// walkTypeSteps renders a path's remaining segments inside a value's type.
// Every step must be a reading the type itself justifies — a member of an
// object or map, an index into a list or tuple — and exactly one such reading
// may survive; two are ambiguity the payload cannot resolve, and none is a
// path the type refuses.
func walkTypeSteps(declared cty.Type, steps []string, need terminal) (string, error) {
	suffix, readings := uniqueReading(declared, steps, need)
	if readings != 1 {
		return "", nestedValue(strings.Join(steps, "."))
	}

	return suffix, nil
}

// uniqueReading resolves a typed path to exactly one reading, down to a leaf
// the form accepts. Each step enumerates the readings its type justifies and
// the rest of the path filters them.
func uniqueReading(declared cty.Type, steps []string, need terminal) (string, int) {
	if len(steps) == 0 {
		if need.accepts(declared) != nil {
			return "", 0
		}

		return "", 1
	}

	readings, ok := stepReads(declared, steps)
	if !ok {
		return "", 0
	}

	spelled := ""
	winners := 0

	for _, candidate := range readings {
		continuation, matches := uniqueReading(candidate.declared, steps[candidate.consumed:], need)
		if matches == 0 {
			continue
		}

		winners += matches
		if winners > 1 {
			return "", 2
		}

		spelled = candidate.spelling + continuation
	}

	return spelled, winners
}

// stepReads renders one step of a typed walk: the readings the type itself
// justifies for the path's leading segments. The bool reports whether any
// reading exists.
func stepReads(declared cty.Type, steps []string) ([]stepReading, bool) {
	switch {
	case declared.IsListType():
		key, isIndex := pureIndex(steps[0])
		if !isIndex {
			return nil, false
		}

		return []stepReading{{spelling: "[" + key + "]", declared: declared.ElementType(), consumed: 1}}, true
	case declared.IsTupleType():
		key, isIndex := pureIndex(steps[0])
		if !isIndex {
			return nil, false
		}

		position, err := strconv.Atoi(key)
		if err != nil || position >= len(declared.TupleElementTypes()) {
			return nil, false
		}

		return []stepReading{{
			spelling: "[" + key + "]", declared: declared.TupleElementTypes()[position], consumed: 1,
		}}, true
	case declared.IsSetType():
		return nil, false
	case declared.IsMapType():
		return keyedReads(declared.ElementType(), nil, steps)
	case declared.IsObjectType():
		return keyedReads(cty.NilType, declared.AttributeTypes(), steps)
	default:
		return nil, false
	}
}

// keyedReads renders a keyed collection's step. The key may span several
// payload segments — a dot is a member of a Kubernetes label key as much as a
// path separator — so every split of the step window is a candidate, and the
// rest of the path decides which of them is real.
func keyedReads(
	element cty.Type,
	attributes map[string]cty.Type,
	steps []string,
) ([]stepReading, bool) {
	var readings []stepReading

	// The fused reading: `aliases[0]` may name the member `aliases` indexed
	// by 0, which the member's own type has to justify.
	name, key, indexed := cutIndex(steps[0])
	if indexed && name != "" {
		if member, found := keyedMember(element, attributes, []string{name}); found {
			declared, err := indexType(member, name, key)
			if err == nil {
				readings = append(readings, stepReading{
					spelling: memberSpelling(name) + "[" + key + "]", declared: declared, consumed: 1,
				})
			}
		}
	}

	// The whole-window readings: the key spans one or more payload segments.
	for split := 1; split <= len(steps); split++ {
		declared, found := keyedMember(element, attributes, steps[:split])
		if !found {
			continue
		}

		readings = append(readings, stepReading{
			spelling: memberSpelling(strings.Join(steps[:split], ".")), declared: declared, consumed: split,
		})
	}

	return readings, len(readings) > 0
}

// keyedMember resolves a candidate key against a keyed collection's element
// type: any key for a map, a named attribute for an object.
func keyedMember(
	element cty.Type,
	attributes map[string]cty.Type,
	key []string,
) (cty.Type, bool) {
	if attributes == nil {
		return element, true
	}

	declared, found := attributes[strings.Join(key, ".")]

	return declared, found
}

// memberSpelling spells a member reference: a valid identifier stays dotted,
// everything else — a Kubernetes label key, a literal key spelled like an
// index — reads in index form, because the dotted spelling would assert a
// different attribute.
func memberSpelling(key string) string {
	if discovery.ValidIdentifier(key) {
		return "." + key
	}

	return "[" + quoteKey(key) + "]"
}

// indexType resolves a bracketed step against the type it indexes.
func indexType(declared cty.Type, name, key string) (cty.Type, error) {
	if !digitsOnly(key) {
		return cty.NilType, indexRefusal(declared, name+"["+key+"]")
	}

	switch {
	case declared.IsListType():
		return declared.ElementType(), nil
	case declared.IsTupleType():
		position, err := strconv.Atoi(key)
		if err != nil || position >= len(declared.TupleElementTypes()) {
			return cty.NilType, indexRefusal(declared, name+"["+key+"]")
		}

		return declared.TupleElementTypes()[position], nil
	case declared.IsSetType():
		return cty.NilType, fmt.Errorf("%w: the provider types %s as %s, and a set has "+
			"no index an assertion could name", ErrUnrenderable, name, declared.FriendlyName())
	default:
		return cty.NilType, fmt.Errorf("%w: the provider types %s as %s, which cannot be "+
			"indexed by %s", ErrUnrenderable, name, declared.FriendlyName(), key)
	}
}

// pureIndex reports whether a payload segment is nothing but a numeric index.
func pureIndex(segment string) (key string, ok bool) {
	name, index, indexed := cutIndex(segment)
	if !indexed || name != "" {
		return "", false
	}

	return index, digitsOnly(index)
}

// leafRefusal's work now belongs to `terminal.accepts`: the form that asks
// for the walk decides what may sit at its end.

func indexRefusal(declared cty.Type, segment string) error {
	return fmt.Errorf("%w: the provider types the value as %s, which cannot be "+
		"indexed the way %s does", ErrUnrenderable, declared.FriendlyName(), segment)
}

// nestedValue is the fallback refusal for a path whose segments the walk
// cannot justify: the payload path alone cannot tell a map key from an object
// attribute.
func nestedValue(attribute string) error {
	return fmt.Errorf("%w: %s is a nested value, and the payload path alone "+
		"cannot tell a map key from an object attribute", ErrUnrenderable, attribute)
}

func notDotted(expression string) error {
	return fmt.Errorf("%w: %s has no supported traversal spelling",
		ErrUnrenderable, expression)
}

// walkOutputPath renders an output's element path from the type the baseline
// publishes for it. Every step must be a reading the type itself justifies —
// a member of an object or map, an index into a list or tuple — and exactly
// one such reading may survive. Where nothing types the output the walk keeps
// the grammar floor, the only path the payload alone can justify.
func walkOutputPath(attribute string, segments []string, outputType cty.Type, need terminal) (string, error) {
	if outputType == cty.NilType {
		return grammarFloor(attribute, segments)
	}

	return walkTypeSteps(outputType, segments, need)
}

// grammarFloor is the output walk where nothing types the output: one
// identifier member, or nothing. A bracketed or multi-segment path needs type
// evidence, and there is none.
func grammarFloor(attribute string, segments []string) (string, error) {
	if len(segments) != 1 {
		return "", nestedValue(attribute)
	}

	name, _, indexed := cutIndex(segments[0])
	if indexed || !discovery.ValidIdentifier(name) {
		return "", nestedValue(attribute)
	}

	return "." + name, nil
}

// quoteKey renders a payload key as an HCL quoted string.
//
// HCL's escape set is narrower than Go's — a string literal accepts only
// `\n`, `\r`, `\t`, `\"`, `\\`, `\uXXXX` and `\UXXXXXXXX`, and a rune
// outside the set is an "Invalid escape sequence" diagnostic, so Go's own
// quoting cannot be reused: its `\b`, `\f`, `\v` and `\a` are diagnostics
// here. Every other rune is written as itself, and inside HCL a `${` or `%{`
// starts an interpolation or directive even in a quoted string, so only the
// doubled marker spells it literally.
func quoteKey(key string) string {
	builder := &strings.Builder{}
	builder.WriteByte('"')

	for index, letter := range key {
		switch letter {
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '$', '%':
			builder.WriteRune(letter)
			if index+1 < len(key) && key[index+1] == '{' {
				builder.WriteRune(letter)
			}
		default:
			if letter < 0x20 || letter == 0x7f {
				fmt.Fprintf(builder, `\u%04x`, letter)

				break
			}

			builder.WriteRune(letter)
		}
	}

	builder.WriteByte('"')

	return builder.String()
}

// digitsOnly reports whether a bracketed key is a numeric index.
func digitsOnly(key string) bool {
	if key == "" {
		return false
	}

	for _, letter := range key {
		if letter < '0' || letter > '9' {
			return false
		}
	}

	return true
}

// leafEquality renders a single-segment attribute, which is either a scalar
// leaf or one element of a collection named by a concrete key.
func (r render) leafEquality(expression, resource, segment, baseline string) (string, error) {
	// A leaf always descends into a value — an attribute, or an element of
	// one — so its equality reads through the absence-tolerant form: a mutant
	// that removes what it reads must fail the assertion, not error the run.
	expression = tolerant(expression)

	name, key, indexed := cutIndex(segment)

	kind, resourceType, ok := schemaCoordinates(resource)
	if !ok {
		if indexed {
			return "", fmt.Errorf("%w: %s indexes a collection nothing types, and an "+
				"index into a set is not legal Terraform", ErrUnrenderable, segment)
		}

		return scalarEquality(expression, baseline)
	}

	described, _ := r.schemas.Attribute(kind, resourceType, []string{name})
	declared, typed := described.ValueType()

	if !indexed {
		if typed && !declared.IsPrimitiveType() {
			return "", fmt.Errorf("%w: the provider types %s as %s, which no scalar "+
				"equality can express", ErrUnrenderable, name, declared.FriendlyName())
		}

		return scalarEquality(expression, baseline)
	}

	if !typed {
		return "", fmt.Errorf("%w: no provider schema types %s, so %s cannot be told "+
			"apart from a set, which has no index", ErrUnrenderable, name, segment)
	}

	if !declared.IsListType() && !declared.IsTupleType() {
		return "", fmt.Errorf("%w: the provider types %s as %s, which cannot be "+
			"indexed by %s", ErrUnrenderable, name, declared.FriendlyName(), key)
	}

	return scalarEquality(expression, baseline)
}

// scalarEquality renders a scalar leaf back into Terraform syntax.
//
// Rendering goes through `cty` and `hclwrite.TokensForValue`, which is exactly
// correct for the primitives and exactly the thing that cannot be trusted for
// collections. A typed null is skipped: the payload's `null` has lost its type,
// and a comparison that silently changes it is the class of defect this
// contract exists to prevent.
func scalarEquality(expression, baseline string) (string, error) {
	value, err := scalarValue(baseline)
	if err != nil {
		return "", err
	}

	return expression + " == " + strings.TrimSpace(
		string(hclwrite.TokensForValue(value).Bytes()),
	), nil
}

func scalarValue(baseline string) (cty.Value, error) {
	switch {
	case baseline == nullMarker:
		return cty.NilVal, fmt.Errorf("%w: the payload's null has lost its type, and a "+
			"typed null is not expressible as an equality", ErrUnrenderable)
	case baseline == "true" || baseline == "false":
		return cty.BoolVal(baseline == "true"), nil
	case strings.HasPrefix(baseline, `"`):
		text, err := strconv.Unquote(baseline)
		if err != nil {
			return cty.NilVal, fmt.Errorf("%w: %s is not a rendering this generator "+
				"can decode", ErrUnrenderable, baseline)
		}

		return cty.StringVal(text), nil
	default:
		number, err := cty.ParseNumberVal(baseline)
		if err != nil {
			return cty.NilVal, fmt.Errorf("%w: %s is neither a scalar leaf nor an "+
				"empty collection", ErrUnrenderable, baseline)
		}

		return number, nil
	}
}

// schemaCoordinates splits an addressable prefix into the schema lookup it
// implies, or reports that it names nothing a provider schema describes.
func schemaCoordinates(resource string) (kind, resourceType string, ok bool) {
	parts := strings.Split(stripKeys(resource), ".")

	if parts[0] == "data" && len(parts) >= dataAddressParts {
		return "data", parts[1], true
	}

	if parts[0] == "output" || len(parts) < resourceAddressParts {
		return "", "", false
	}

	return "resource", parts[0], true
}

// The segment counts of the two addressable block shapes.
const (
	dataAddressParts     = 3
	resourceAddressParts = 2
)

// cutIndex separates an attribute segment from the element key it indexes.
func cutIndex(segment string) (name, key string, indexed bool) {
	open := strings.Index(segment, "[")
	if open < 0 || !strings.HasSuffix(segment, "]") {
		return segment, "", false
	}

	return segment[:open], segment[open+1 : len(segment)-1], true
}

// stripKeys removes instance keys from an address, for a schema lookup that is
// about the type and never about the instance.
func stripKeys(address string) string {
	builder := strings.Builder{}
	depth := 0

	for _, letter := range address {
		switch {
		case letter == '[':
			depth++
		case letter == ']':
			depth--
		case depth == 0:
			_, _ = builder.WriteRune(letter)
		default:
		}
	}

	return builder.String()
}
