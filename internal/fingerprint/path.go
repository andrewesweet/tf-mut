package fingerprint

import (
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// Path is one canonical payload path, parsed once into the records its
// questions are answered from.
//
// A canonical path is a flattened key of a payload's value map: a plan or
// state resource record (`resource_changes[ADDRESS].change…`,
// `root_module.resources[ADDRESS].values…`), a plan or state output record
// (`output_changes.NAME.after…`, `outputs.NAME.value…`), or one of the
// payload's own bookkeeping members. The grammar is this package's, so
// consumers ask a parsed path instead of rebuilding its prefixes at their call
// sites — a rebuilt prefix is the adapter joining two address spaces without
// an adapter, the failure shape the M3 spec review named twice.
type Path struct {
	// raw is the canonical spelling the value was parsed from. The questions
	// that match a whole spelling rather than project a part of it — the
	// attribute question against the path it is asked of — read it.
	raw string
	// kind is the top-level record the path lives in.
	kind pathKind
	// owner is the addressed owner the grammar promises: the resource address
	// between the brackets of a resource path, the output name of an output
	// path. Empty where the parse found no owner.
	owner string
	// body is what follows the owner: a resource path's remainder after the
	// address's closing bracket, its leading dot kept, or an output path's
	// remainder after the name's separating dot.
	body string
	// head and tail are a resource path cut at the FIRST closing bracket —
	// the cut the unknown-marker grammar reads, which is not the matching-
	// bracket cut whenever the address itself carries an index.
	head string
	tail string
}

// pathKind names the top-level record a canonical path lives in.
type pathKind int

const (
	// pathOther is the payload's own bookkeeping — `format_version` and every
	// member the grammar does not name, including a `root_module` member that
	// is not a resource record.
	pathOther pathKind = iota
	pathPlanResource
	pathPlanOutput
	pathStateResource
	pathStateOutput
)

// ParsePath parses one canonical payload path.
//
// The parse is total: a path the grammar does not name parses to a value
// whose questions are all answered negatively, because a payload's value map
// is keyed by everything Terraform serialised, not only by the paths the
// grammar promises.
func ParsePath(raw string) Path {
	p := Path{raw: raw}

	switch {
	case strings.HasPrefix(raw, resourceChanges+"["):
		p.kind = pathPlanResource
		p.parseResource(strings.TrimPrefix(raw, resourceChanges+"["))
	case strings.HasPrefix(raw, outputChanges+"."):
		p.kind = pathPlanOutput
		p.parseOutput(strings.TrimPrefix(raw, outputChanges+"."))
	case strings.HasPrefix(raw, rootModule+".resources["):
		p.kind = pathStateResource
		p.parseResource(strings.TrimPrefix(raw, rootModule+".resources["))
	case strings.HasPrefix(raw, stateOutputs+"."):
		p.kind = pathStateOutput
		p.parseOutput(strings.TrimPrefix(raw, stateOutputs+"."))
	}

	return p
}

// parseResource records a resource path's bracketed address, the remainder
// after its matching close, and the first-close cut the marker grammar reads.
func (p *Path) parseResource(rest string) {
	p.head, p.tail, _ = strings.Cut(rest, "]")
	p.owner, p.body, _ = cutBracketed(rest)
}

// parseOutput records an output path's name and the remainder after the dot
// separating it from the name's first member. A path that ends at the name
// leaves the remainder empty.
func (p *Path) parseOutput(rest string) {
	p.owner, p.body, _ = strings.Cut(rest, ".")
}

// Address projects the path onto the Terraform address a reader would
// recognise, which is what every diagnosis is expressed in.
//
// A path that names no address — the payload's own bookkeeping — projects to
// empty.
func (p Path) Address() string {
	switch p.kind {
	case pathPlanResource:
		return p.addressUnder("change.after", "change.before")
	case pathStateResource:
		return p.addressUnder(planValues)
	case pathPlanOutput, pathStateOutput:
		return p.outputAddress()
	default:
		return ""
	}
}

// addressUnder projects a resource path onto its address, extended by the
// attribute beneath whichever plan or state body the path continues from. A
// path that continues under no body projects to the bare address.
func (p Path) addressUnder(bodies ...string) string {
	rest := strings.TrimPrefix(p.body, ".")

	for _, body := range bodies {
		if rest == body {
			return p.owner
		}

		if attribute, ok := strings.CutPrefix(rest, body+"."); ok {
			return p.owner + "." + attribute
		}
	}

	return p.owner
}

// outputAddress projects an output path onto the output's Terraform address.
func (p Path) outputAddress() string {
	if p.owner == "" {
		return ""
	}

	address, _ := discovery.BlockAddr(outputRoot, []string{p.owner})

	return address
}

// Attribute reports whether the path is exactly the given argument of the
// given resource — the attribute itself, or any member beneath it — in either
// the plan or the state projection.
//
// The question is a match against a whole spelling rather than a projection
// of one part, so it is answered against the parsed owner and body: an
// address is a Terraform address, and a path answers only where its own
// address is the one asked about.
func (p Path) Attribute(address, attribute string) bool {
	rest := strings.TrimPrefix(p.body, ".")

	var bodies []string

	switch p.kind {
	case pathPlanResource:
		bodies = []string{changeAfter, changeBefore}
	case pathStateResource:
		bodies = []string{planValues}
	default:
		return false
	}

	if p.owner != address {
		return false
	}

	for _, body := range bodies {
		if rest == body+"."+attribute || strings.HasPrefix(rest, body+"."+attribute+".") {
			return true
		}
	}

	return false
}

// Segments splits the path's attribute on its top-level dots, keeping
// instance and element keys attached to the segment they index.
//
// The split is the grammar's own, so a renderer consumes segments instead of
// re-splitting the attribute at its call site. A path that names no assertable
// value — one whose Split fails — has no attribute to split and reports none.
func (p Path) Segments() []string {
	_, attribute, ok := p.Split()
	if !ok {
		return nil
	}

	return attributeSegments(attribute)
}

// Split separates the path into the Terraform address a test assertion could
// name and the attribute path beneath it.
//
// A path in a plan's prior state, or inside a child module's state, reports
// false: no assertion can read either.
func (p Path) Split() (address, attribute string, ok bool) {
	switch p.kind {
	case pathPlanResource:
		return p.splitAddressed(changeAfter)
	case pathStateResource:
		return p.splitAddressed(planValues)
	case pathPlanOutput:
		return p.splitOutput(outputAfterField)
	case pathStateOutput:
		return p.splitOutput(outputValueField)
	default:
		return "", "", false
	}
}

// splitAddressed separates a resource path whose attribute continues beneath
// one plan or state body.
func (p Path) splitAddressed(body string) (address, attribute string, ok bool) {
	rest := strings.TrimPrefix(p.body, ".")

	if rest == body {
		return p.owner, "", true
	}

	if attribute, cut := strings.CutPrefix(rest, body+"."); cut {
		return p.owner, attribute, true
	}

	return "", "", false
}

// splitOutput separates an output path. A list-valued output's element path
// continues from the body with an index, not a dot:
// `outputs.X.value[0].attr`, `output_changes.X.after[0].attr`. The index
// travels in the attribute so the rendered address reads `output.X[0].attr`.
// Only the value body enters this branch; metadata fields stay outside the
// assertion surface.
func (p Path) splitOutput(body string) (address, attribute string, ok bool) {
	if p.owner == "" {
		return "", "", false
	}

	address, _ = discovery.BlockAddr(outputRoot, []string{p.owner})

	if p.body == body {
		return address, "", true
	}

	if attribute, cut := strings.CutPrefix(p.body, body+"."); cut {
		return address, attribute, true
	}

	if attribute, cut := strings.CutPrefix(p.body, body+"["); cut {
		key, _, found := strings.Cut(attribute, "]")
		if found && digitsOnly(key) {
			return address, "[" + attribute, true
		}
	}

	return "", "", false
}

// IsUnknownMarker reports whether the path is — or lives under — a plan's
// unknown marker: the record that says which parts of a value the baseline
// could not know. The markers are evidence about a comparison, never a value
// to assert, and a renderer that reads them as a value's shape renders a lie.
func (p Path) IsUnknownMarker() bool {
	_, found := p.UnknownValueBase()

	return found
}

// UnknownValueBase maps an unknown-marker path onto the value path whose
// subtree it marks: `output_changes.X.after_unknown…` marks
// `output_changes.X.after…`, and a resource change's `change.after_unknown…`
// marks its `change.after…`. A path that is not a marker path reports false.
//
// The marker grammar reads a resource path at the first closing bracket, not
// the matching one, so a marker under an indexed instance's address is not a
// marker path — that is the grammar's own answer, not an accident of the
// parse.
func (p Path) UnknownValueBase() (string, bool) {
	switch p.kind {
	case pathPlanOutput:
		return p.unknownOutputBase()
	case pathPlanResource:
		marked, isMarker := strings.CutPrefix(p.tail, "."+changeAfterUnknown)
		if !isMarker {
			return "", false
		}

		return resourceChanges + "[" + p.head + "]." + changeAfter + marked, true
	default:
		return "", false
	}
}

// unknownOutputBase maps an output-side marker onto its marked value: the
// output name and the bracketed element key, if the marker carries one,
// transfer verbatim onto the after path.
func (p Path) unknownOutputBase() (string, bool) {
	marked, isMarker := strings.CutPrefix(p.body, afterUnknown)
	if !isMarker {
		return "", false
	}

	// A whole element the plan marks unknown carries its index in the marker:
	// after_unknown[0].id marks after[0].id, the element's member, not a
	// member of the collection itself. The bracketed key is consumed
	// depth-matched, and what follows must be empty or dotted.
	if rest, cut := strings.CutPrefix(marked, "["); cut {
		index, remainder, closed := cutBracketed(rest)
		if !closed || (remainder != "" && remainder[0] != '.') {
			return "", false
		}

		marked = "[" + index + "]" + remainder
	} else if marked != "" && marked[0] != '.' {
		return "", false
	}

	return outputChanges + "." + p.owner + "." + outputAfterField + marked, true
}

// IsOutputMetadata reports whether the path names an output record's metadata
// — its type descriptor or a sensitivity or unknown marker — rather than its
// value.
//
// The metadata members change whenever the value does, and they are never
// assertable; a candidate selector asks the question to prefer the value
// delta, whose refusal would otherwise be outranked in the report by the
// metadata's own.
func (p Path) IsOutputMetadata() bool {
	switch p.kind {
	case pathPlanOutput, pathStateOutput:
	default:
		return false
	}

	if p.body == "" {
		return false
	}

	field, _, _ := strings.Cut(p.body, ".")
	if bracket := strings.IndexByte(field, '['); bracket >= 0 {
		field = field[:bracket]
	}

	// Only the metadata members are unassertable; the value and its change
	// body are not, and neither is anything the grammar does not name.
	switch field {
	case outputSensitive, outputType,
		afterSensitive, beforeSensitive, afterUnknown, beforeUnknown:
		return true
	default:
		return false
	}
}

// CountedInstance reads a state or plan resource path onto the counted
// instance it names: `root_module.resources[null_resource.app[1]].values…`
// onto resource `null_resource.app`, index 1, and a resource change's
// `resource_changes[null_resource.app[0]].change…` likewise. A path whose
// address carries no numeric instance index — a singleton resource, a
// string-keyed `for_each` instance, a value outside any resource — reports
// false. `count` is the only instance numbering this can read, which is the
// point: a vanishing instance is what a count mutation does.
func (p Path) CountedInstance() (resource string, index int, ok bool) {
	switch p.kind {
	case pathPlanResource, pathStateResource:
	default:
		return "", 0, false
	}

	name, tail, hasIndex := strings.Cut(p.owner, "[")
	if !hasIndex || !strings.HasSuffix(tail, "]") || name == "" {
		return "", 0, false
	}

	key := strings.TrimSuffix(tail, "]")
	if !digitsOnly(key) {
		return "", 0, false
	}

	index, err := strconv.Atoi(key)
	if err != nil {
		return "", 0, false
	}

	return name, index, true
}

// CountedInstanceRoot spells the state or plan path at which Terraform records the
// numbered instance of a resource — the root record whose vanished change is
// the only change that can prove the instance vanished.
//
// The root is the path's own first bracket, matched or not: the question is
// about the spelling a change is recorded under, not about a parsed owner, so
// it is answered on the canonical string.
func (p Path) CountedInstanceRoot() string {
	start := strings.IndexByte(p.raw, '[') + 1
	address, _, _ := cutBracketed(p.raw[start:])

	return p.raw[:start] + address + "]"
}

// attributeSegments splits an attribute path on its top-level dots, keeping
// instance and element keys attached to the segment they index.
func attributeSegments(attribute string) []string {
	if attribute == "" {
		return nil
	}

	segments := []string{}
	builder := strings.Builder{}
	depth := 0

	for _, letter := range attribute {
		switch {
		case letter == '[':
			depth++
		case letter == ']':
			depth--
		case letter == '.' && depth == 0:
			segments = append(segments, builder.String())
			builder.Reset()

			continue
		default:
		}

		_, _ = builder.WriteRune(letter)
	}

	return append(segments, builder.String())
}
