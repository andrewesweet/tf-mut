// Package fingerprint reduces the per-run plan and state payloads of a verbose
// `terraform test` run to the canonical, volatility-masked form the oracle
// compares.
//
// Everything here is a projection of documents Terraform produced: the package
// never decides a verdict, it decides what two runs can honestly be said to
// have in common.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// Accepted payload format versions.
//
// The oracle rests on documents whose shape carries no compatibility promise,
// so the accepted range is pinned and a payload outside it is a loud
// operational failure rather than a verdict (product design open question 6).
const (
	minPlanFormatVersion  = "1.0"
	maxPlanFormatVersion  = "1.2"
	minStateFormatVersion = "1.0"
	maxStateFormatVersion = "1.0"
)

// ErrFormatVersion reports a payload outside the pinned version range.
var ErrFormatVersion = errors.New("unsupported terraform payload format version")

// ErrPayload reports a payload the canonicaliser could not read.
var ErrPayload = errors.New("terraform payload could not be canonicalised")

// The payload members that carry the format version of each kind.
const (
	planVersionField  = "plan_format_version"
	stateVersionField = "state_format_version"
)

// volatileMarker stands in for a component the mask removed. It is not a legal
// character in Terraform's JSON output, so it can never collide with a value.
const volatileMarker = "\x00"

// Payload is one run block's canonical projection: a flat map from canonical
// path to canonical scalar.
//
// Flattening is what makes the rest of the oracle simple: masking, delta
// computation and hashing are all operations on this one map.
type Payload struct {
	// File is the test file the run block belongs to.
	File string
	// Run is the run block name.
	Run string
	// Kind is plan or state.
	Kind string
	// Values maps canonical path to canonical scalar rendering.
	Values map[string]string
	// Unknowns lists the addresses whose value the payload reports as unknown.
	Unknowns []string
	// OutputTypes retains published type evidence for rendering output indices.
	// Missing or invalid type descriptors supply no evidence.
	OutputTypes map[string]cty.Type
}

// Key identifies the run a payload belongs to, and orders the composition.
func (p Payload) Key() string {
	return p.File + "::" + p.Run
}

// Canonicalise projects the decoded payloads of one verbose run.
//
// Payloads arrive in stream order; the returned slice is ordered by run
// identity so that the composed fingerprint cannot depend on execution order.
func Canonicalise(payloads []tfexec.RunPayload) ([]Payload, error) {
	canonical := make([]Payload, 0, len(payloads))

	for _, payload := range payloads {
		projected, err := project(payload)
		if err != nil {
			return nil, err
		}

		canonical = append(canonical, projected)
	}

	slices.SortFunc(canonical, func(left, right Payload) int {
		return strings.Compare(left.Key(), right.Key())
	})

	return canonical, nil
}

func project(payload tfexec.RunPayload) (Payload, error) {
	if err := checkFormatVersion(payload); err != nil {
		return Payload{}, err
	}

	values := map[string]string{}
	flatten("", payload.Value, values)

	return Payload{
		File:        payload.File,
		Run:         payload.Run,
		Kind:        payload.Kind,
		Values:      values,
		Unknowns:    unknownAddresses(values),
		OutputTypes: publishedOutputTypes(payload),
	}, nil
}

func publishedOutputTypes(payload tfexec.RunPayload) map[string]cty.Type {
	values := payload.Value
	types := map[string]cty.Type{}
	if payload.Kind == tfexec.PayloadPlan {
		planned, ok := values["planned_values"].(map[string]any)
		if !ok {
			return types
		}

		values = planned
	}

	outputs, ok := values["outputs"].(map[string]any)
	if !ok {
		return types
	}

	for name, value := range outputs {
		output, ok := value.(map[string]any)
		if !ok {
			continue
		}

		encoded, err := json.Marshal(output["type"])
		if err != nil {
			continue
		}

		declared, err := ctyjson.UnmarshalType(encoded)
		if err == nil {
			address, _ := discovery.BlockAddr(outputRoot, []string{name})
			types[address] = declared
		}
	}

	return types
}

func checkFormatVersion(payload tfexec.RunPayload) error {
	field, minimum, maximum := planVersionField, minPlanFormatVersion, maxPlanFormatVersion
	if payload.Kind == tfexec.PayloadState {
		field, minimum, maximum = stateVersionField, minStateFormatVersion, maxStateFormatVersion
	}

	raw, found := payload.Value[field]
	if !found {
		return fmt.Errorf("%w: %s payload declares no %s", ErrFormatVersion, payload.Kind, field)
	}

	version, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: %s is %T, not a string", ErrFormatVersion, field, raw)
	}

	if compareVersions(version, minimum) < 0 || compareVersions(version, maximum) > 0 {
		return fmt.Errorf("%w: %s is %s, and this build reads %s to %s; "+
			"a payload outside the pinned range is never classified",
			ErrFormatVersion, field, version, minimum, maximum)
	}

	return nil
}

// compareVersions orders dotted numeric versions, shorter meaning zero-padded.
func compareVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")

	for index := range max(len(leftParts), len(rightParts)) {
		leftPart := versionPart(leftParts, index)
		rightPart := versionPart(rightParts, index)

		if leftPart != rightPart {
			return leftPart - rightPart
		}
	}

	return 0
}

func versionPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}

	value, err := strconv.Atoi(parts[index])
	if err != nil {
		return -1
	}

	return value
}

// The canonical renderings of the two containers that have no leaves. Absent
// and empty must be distinguishable, so an empty container emits a value of its
// own rather than nothing.
const (
	emptyObject = "{}"
	emptyArray  = "[]"
)

// bookkeeping members of a resource entry or a change record that no assertion
// can reach.
//
// The oracle's claim is about what an assertion over the serialised plan or
// state could distinguish, and a `terraform test` assertion evaluates HCL
// against the module: it can read a resource's values and which instances
// exist, and it has no expression at all for the provider that served it, the
// schema version it was written with, or its `depends_on` edges.
//
// Every entry was checked against the real binary rather than reasoned about,
// and that check removed one candidate. `depends_on` is here because
// `terraform test -verbose -json` v1.15.8 serialises it into `test_state`,
// which would otherwise make every DEPENDS-DROP mutant look observable while
// remaining, by construction, unassertable. The sensitivity members are *not*
// here, although they read like bookkeeping: `issensitive` is a Terraform
// function, an assertion over it passes, and dropping them would have made
// every sensitivity mutant invisible to the oracle.
//
//nolint:gochecknoglobals // an immutable lookup table.
var unreachableMembers = map[string]bool{
	"provider_name":       true,
	"schema_version":      true,
	"mode":                true,
	"depends_on":          true,
	"replace_paths":       true,
	"deposed_key":         true,
	"tainted":             true,
	"provider_config_key": true,
}

// reachable reports whether a member of the payload is one an assertion could
// observe, given where in the document it sits.
//
// Position matters: `mode` is bookkeeping on a resource entry and an ordinary
// argument inside `values`, and only the first is dropped.
func reachable(prefix, key string) bool {
	if prefix == "" {
		return key != "relevant_attributes"
	}

	if !unreachableMembers[key] {
		return true
	}

	return !isEntry(prefix) && !isChange(prefix)
}

// isEntry reports a path that names one resource of an addressed collection.
func isEntry(prefix string) bool {
	return strings.HasSuffix(prefix, "]") &&
		(strings.Contains(prefix, resourceChanges+"[") || strings.Contains(prefix, "resources["))
}

// isChange reports the change record of such an entry.
func isChange(prefix string) bool {
	trimmed, found := strings.CutSuffix(prefix, ".change")

	return found && isEntry(trimmed)
}

// flatten walks a decoded payload into canonical path/scalar pairs.
//
// Object keys are sorted by construction — a map has no order, and the path is
// the key. Arrays keep payload order, except that an array whose elements each
// carry an `address` is keyed by that address instead: `resource_changes` and
// the state's resource lists are addressed collections, and letting an inserted
// resource shift every index would turn one mutation into a whole-payload
// delta.
func flatten(prefix string, value any, into map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			into[prefix] = emptyObject

			return
		}

		for key, member := range typed {
			if !reachable(prefix, key) {
				continue
			}

			flatten(join(prefix, key), member, into)
		}
	case []any:
		flattenArray(prefix, typed, into)
	default:
		into[prefix] = scalar(value)
	}
}

func flattenArray(prefix string, elements []any, into map[string]string) {
	if len(elements) == 0 {
		into[prefix] = emptyArray

		return
	}

	if addresses, ok := addressesOf(elements); ok {
		for index, element := range elements {
			flatten(prefix+"["+addresses[index]+"]", element, into)
		}

		return
	}

	for index, element := range elements {
		flatten(prefix+"["+strconv.Itoa(index)+"]", element, into)
	}
}

// addressesOf reports the element addresses of an addressed collection, and
// whether the elements form one at all.
func addressesOf(elements []any) ([]string, bool) {
	addresses := make([]string, 0, len(elements))
	seen := map[string]bool{}

	for _, element := range elements {
		object, ok := element.(map[string]any)
		if !ok {
			return nil, false
		}

		address, ok := object["address"].(string)
		if !ok || address == "" || seen[address] {
			return nil, false
		}

		seen[address] = true

		addresses = append(addresses, address)
	}

	return addresses, true
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}

// scalar renders a leaf so that null, absent, "null" and 0 are all distinct.
func scalar(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(typed)
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}

		return string(encoded)
	}
}

// The payload members that mark a value as not yet known.
const (
	afterUnknown  = "after_unknown"
	beforeUnknown = "before_unknown"
)

// unknownAddresses names every construct the payload reports as unknown.
//
// The address matters as much as the count: it is the evidence the
// `indeterminate-unknown-values` diagnosis carries, and the reason the oracle
// refuses to call the mutant unobservable.
func unknownAddresses(values map[string]string) []string {
	found := map[string]bool{}

	for path, value := range values {
		if value != "true" {
			continue
		}

		address, ok := unknownAddress(path)
		if !ok {
			continue
		}

		found[address] = true
	}

	addresses := make([]string, 0, len(found))
	for address := range found {
		addresses = append(addresses, address)
	}

	slices.Sort(addresses)

	return addresses
}

func unknownAddress(path string) (string, bool) {
	segments := strings.Split(path, ".")

	for index, segment := range segments {
		if segment != afterUnknown && segment != beforeUnknown {
			continue
		}

		owner := ParsePath(strings.Join(segments[:index], ".")).Address()
		if owner == "" {
			return "", false
		}

		rest := segments[index+1:]
		if len(rest) == 0 {
			return owner, true
		}

		return owner + "." + strings.Join(rest, "."), true
	}

	return "", false
}

// cutBracketed splits the contents of an already-opened bracket from what
// follows its matching close.
//
// Cutting at the first `]` would be wrong for every indexed instance: the
// address inside `resource_changes[terraform_data.app[0]]` carries a bracket of
// its own, and stopping at the inner one yields an address that names nothing.
func cutBracketed(rest string) (inside, remainder string, ok bool) {
	depth := 1

	for index, letter := range rest {
		switch letter {
		case '[':
			depth++
		case ']':
			depth--
		default:
			continue
		}

		if depth == 0 {
			return rest[:index], rest[index+1:], true
		}
	}

	return "", "", false
}

// The payload members whose contents are addressed by Terraform address.
const (
	resourceChanges = "resource_changes"
	outputChanges   = "output_changes"
	rootModule      = "root_module"
	stateOutputs    = "outputs"
	// outputRoot is the address root of a module output.
	outputRoot = "output"
)

// Fingerprint composes the per-run hashes into the mutant's fingerprint.
//
// Composition is over run identity rather than execution order, so parallelism
// and file order cannot reach the result.
func Fingerprint(payloads []Payload) string {
	digest := sha256.New()

	ordered := make([]Payload, len(payloads))
	copy(ordered, payloads)

	slices.SortFunc(ordered, func(left, right Payload) int {
		return strings.Compare(left.Key(), right.Key())
	})

	for _, payload := range ordered {
		_, _ = fmt.Fprintf(digest, "%s\n%s\n", payload.Key(), hashValues(payload.Values))
	}

	return hex.EncodeToString(digest.Sum(nil))
}

func hashValues(values map[string]string) string {
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	digest := sha256.New()
	for _, path := range paths {
		_, _ = fmt.Fprintf(digest, "%s=%s\n", path, values[path])
	}

	return hex.EncodeToString(digest.Sum(nil))
}

// The sensitivity mirrors Terraform publishes beside every value it serialises.
//
// A plan's `change.after` is mirrored by `change.after_sensitive`, a state
// resource's `values` by its `sensitive_values`, and an output carries its own
// flag. The mirror is a document of the same shape with `true` at each
// sensitive position, so the question "is this value sensitive" is answered by
// reading the same path out of the mirror — and out of every ancestor of it,
// because a sensitive container makes everything under it sensitive.
const (
	afterSensitive     = "after_sensitive"
	beforeSensitive    = "before_sensitive"
	sensitiveValues    = "sensitive_values"
	outputSensitive    = "sensitive"
	outputType         = "type"
	planValues         = "values"
	changeAfter        = "change.after"
	changeAfterUnknown = "change." + afterUnknown
	changeBefore       = "change.before"
	outputAfterField   = "after"
	outputValueField   = "value"
)

// Sensitive reports whether the payload marks the value at a canonical path
// sensitive, at the path itself or at any of its ancestors.
//
// Retaining the metadata is not permission to render the value: `issensitive`
// is a Terraform function and an assertion over it passes, which is why the
// projection keeps these members at all. This predicate is what turns that
// retention into a refusal to inline the value anywhere.
func (p Payload) Sensitive(path string) bool {
	base, segments, ok := sensitivityMirror(ParsePath(path))
	if !ok {
		return false
	}

	for _, candidate := range ancestorPaths(base, segments) {
		if p.Values[candidate] == "true" {
			return true
		}
	}

	return false
}

// SensitiveRenderings collects the canonical rendering of every string value
// the payload marks sensitive.
//
// The path predicate alone is not enough to keep a secret out of a generated
// test file. Terraform propagates a sensitivity mark through its own
// expressions but not through a provider: `terraform_data`'s computed `output`
// carries the value of its sensitive `input` with no mark of its own, measured
// against Terraform v1.15.8. A generated assertion is written into a file
// somebody commits, so the value itself is refused wherever it appears — a
// stricter rule than Terraform's own renderer applies, and deliberately so.
//
// Only string renderings are collected. A sensitive boolean or number would
// otherwise make every `true` and every `0` in the payload unsuggestable, which
// is a cost with no secret behind it.
func (p Payload) SensitiveRenderings() map[string]bool {
	renderings := map[string]bool{}

	for path, value := range p.Values {
		if !strings.HasPrefix(value, `"`) || value == `""` {
			continue
		}

		if p.Sensitive(path) {
			renderings[value] = true
		}
	}

	return renderings
}

// sensitivityMirror maps a canonical value path onto the base of its
// sensitivity mirror and the attribute segments beneath it.
func sensitivityMirror(parsed Path) (base string, segments []string, ok bool) {
	address, attribute, ok := parsed.Split()
	if !ok {
		return "", nil, false
	}

	//nolint:exhaustive // only the four records have mirrors; the default arm is the refusal for the bookkeeping.
	switch parsed.kind {
	case pathPlanResource:
		mirror := afterSensitive
		if strings.Contains(parsed.raw, "]."+changeBefore) {
			mirror = beforeSensitive
		}

		return resourceChanges + "[" + address + "].change." + mirror, attributeSegments(attribute), true
	case pathStateResource:
		return rootModule + ".resources[" + address + "]." + sensitiveValues, attributeSegments(attribute), true
	case pathPlanOutput:
		return outputChanges + "." + strings.TrimPrefix(address, outputRoot+".") + "." + afterSensitive,
			attributeSegments(attribute), true
	case pathStateOutput:
		return stateOutputs + "." + strings.TrimPrefix(address, outputRoot+".") + "." + outputSensitive,
			attributeSegments(attribute), true
	default:
		return "", nil, false
	}
}

// ancestorPaths lists a mirror base and every prefix of the attribute path
// beneath it, so a sensitive container is found from any leaf under it.
func ancestorPaths(base string, segments []string) []string {
	paths := make([]string, 0, 1+len(segments))
	paths = append(paths, base)

	builder := strings.Builder{}
	builder.WriteString(base)

	for _, segment := range segments {
		builder.WriteString("." + segment)
		paths = append(paths, builder.String())
	}

	return paths
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
