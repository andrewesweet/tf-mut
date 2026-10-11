package suggest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/fingerprint"
	"github.com/andrewesweet/tf-mut/internal/tfexec"
)

// ErrUnrenderable reports a value the generator cannot express type-correctly
// for Terraform equality.
//
// The installed `hclwrite.TokensForValue` renders lists, sets and tuples with
// the same `[...]` syntax and maps and objects with the same `{...}` syntax,
// and Terraform v1.15.8 confirms the consequence: `toset(["a"]) == ["a"]` is
// false. Generation is therefore restricted to what renders type-correctly —
// scalar leaves, `length(...)` equalities, and element assertions by concrete
// key — with the provider schema as the type source. Everything else is
// reported as a limit rather than generated and refuted.
var ErrUnrenderable = errors.New("no type-correct Terraform equality renders this value")

// The two canonical renderings that carry no leaf.
const (
	emptyObject = "{}"
	emptyArray  = "[]"
)

// render is the value-rendering contract.
type render struct {
	// schemas is the normative type source. The payload alone cannot
	// distinguish a set from a list, so where a provider types the attribute
	// its type decides, and where nothing types it beyond the scalar in front
	// of us the value is scalar-rendered.
	schemas tfexec.Schemas
}

// equality renders the assert condition for one delta change, or fails closed.
func (r render) equality(parts traversalParts, baseline string) (string, error) {
	expression, resource, attribute := parts.expression, parts.resource, parts.attribute

	if baseline == "" {
		return "", fmt.Errorf("%w: the value is absent from the baseline, and an "+
			"assertion cannot express the absence of a value", ErrUnrenderable)
	}

	// A size delta the baseline proves: the collection was empty. `length` is
	// type-correct over every collection, which is exactly why it is one of the
	// three admitted forms.
	if baseline == emptyObject || baseline == emptyArray {
		return "length(" + expression + ") == 0", nil
	}

	segments := fingerprint.AttributeSegments(attribute)

	switch {
	case attribute == "":
		// A whole output value. No provider schema types an output, so the
		// scalar in front of us is the only type evidence there is.
		return scalarEquality(expression, baseline)
	case strings.HasPrefix(attribute, "[") || len(segments) > 1:
		// The path continues past the value it names: an element of a list-
		// valued output, a nested block, or a keyed collection. The walk
		// renders it from the resource address out, so it starts there —
		// `expression` already carries the provisional dotted spelling and is
		// not reused.
		return r.walkEquality(resource, attribute, baseline)
	default:
		return r.leafEquality(expression, resource, segments[0], baseline)
	}
}

// walkEquality renders a path that continues past a first segment: a list-
// valued output's element, a nested block's attribute, or a keyed collection's
// entry. The walk starts from the resource address and consumes one path
// segment at a time, so every spelling it emits is one the segment in front of
// it justifies.
func (r render) walkEquality(resource, attribute, baseline string) (string, error) {
	kind, resourceType, ok := schemaCoordinates(resource)
	if !ok {
		// No provider schema types an output. The walk trusts exactly what the
		// payload path proves: indices arrived as Terraform wrote them, and a
		// further segment is a traversal only if it is an identifier.
		suffix, err := walkIndicesAndNames(attribute)
		if err != nil {
			return "", err
		}

		expression := resource + suffix
		if err := parseTraversal(expression); err != nil {
			return "", notDotted(expression)
		}

		return scalarEquality(expression, baseline)
	}

	root, found := r.schemas.Block(kind, resourceType)
	if !found {
		return "", nestedValue(attribute)
	}

	builder := &strings.Builder{}
	if err := walkBlockSteps(root, fingerprint.AttributeSegments(attribute), builder); err != nil {
		return "", err
	}

	expression := resource + builder.String()
	if err := parseTraversal(expression); err != nil {
		return "", notDotted(expression)
	}

	return scalarEquality(expression, baseline)
}

// walkBlockSteps renders a payload path's segments inside a block schema's
// namespace. A name resolves against the block's nested block types and
// attributes; the first attribute consumes the remainder of the path in type
// space, because inside a block an attribute is never a map key.
func walkBlockSteps(block tfexec.SchemaBlock, steps []string, builder *strings.Builder) error {
	for i, step := range steps {
		name, key, indexed := cutIndex(step)

		if blockType, found := block.BlockTypes[name]; found {
			piece, nested, err := blockTypeStep(blockType, name, key, indexed)
			if err != nil {
				return err
			}

			builder.WriteString(piece)
			block = nested

			continue
		}

		if _, found := block.Attributes[name]; !found {
			return nestedValue(strings.Join(steps, "."))
		}

		return walkAttributeSteps(block, name, key, indexed, steps[i+1:], builder)
	}

	return nil
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

		return "." + name + "[" + strconv.Quote(key) + "]", blockType.Block, nil
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
// and everything after it is consumed in type space.
func walkAttributeSteps(
	block tfexec.SchemaBlock,
	name, key string,
	indexed bool,
	rest []string,
	builder *strings.Builder,
) error {
	builder.WriteString("." + name)

	declared, known := tfexec.BlockAttributeType(block, name)

	if indexed {
		if !known {
			return fmt.Errorf("%w: no provider schema types %s, so %s cannot be told "+
				"apart from a set, which has no index", ErrUnrenderable, name, name+"["+key+"]")
		}

		element, err := indexType(declared, name, key)
		if err != nil {
			return err
		}

		builder.WriteString("[" + key + "]")

		if len(rest) == 0 {
			return leafRefusal(name+"["+key+"]", element, true)
		}

		return walkTypeSteps(element, rest, builder)
	}

	if len(rest) == 0 {
		return leafRefusal(name, declared, known)
	}

	if !known {
		return fmt.Errorf("%w: no provider schema types %s, so nothing can tell the "+
			"rest of the path from a map key", ErrUnrenderable, name)
	}

	return walkTypeSteps(declared, rest, builder)
}

// walkTypeSteps renders a path's remaining segments inside a value's type.
func walkTypeSteps(declared cty.Type, steps []string, builder *strings.Builder) error {
	if len(steps) == 0 {
		return fmt.Errorf("%w: the value is typed %s, which no scalar equality can "+
			"express", ErrUnrenderable, declared.FriendlyName())
	}

	step := steps[0]

	switch {
	case declared.IsListType():
		key, ok := pureIndex(step)
		if !ok {
			return indexRefusal(declared, step)
		}

		builder.WriteString("[" + key + "]")

		return walkTypeSteps(declared.ElementType(), steps[1:], builder)
	case declared.IsTupleType():
		key, ok := pureIndex(step)
		if !ok {
			return indexRefusal(declared, step)
		}

		types := declared.TupleElementTypes()
		position, err := strconv.Atoi(key)
		if err != nil || position >= len(types) {
			return indexRefusal(declared, step)
		}

		builder.WriteString("[" + key + "]")

		return walkTypeSteps(types[position], steps[1:], builder)
	case declared.IsSetType():
		return fmt.Errorf("%w: the value is typed %s, and a set has no index an "+
			"assertion could name", ErrUnrenderable, declared.FriendlyName())
	case declared.IsMapType():
		return keyedSteps(declared.ElementType(), nil, steps, builder)
	case declared.IsObjectType():
		return keyedSteps(cty.NilType, declared.AttributeTypes(), steps, builder)
	default:
		return fmt.Errorf("%w: the value is typed %s, which has no members for the "+
			"rest of the path to name", ErrUnrenderable, declared.FriendlyName())
	}
}

// keyedSteps renders a keyed collection's remainder: the key may span several
// payload segments — a dot is a member of a Kubernetes label key as much as a
// path separator — so every split of the remainder is a candidate and the
// element type decides which of them is real. Exactly one must survive.
func keyedSteps(
	element cty.Type,
	attributes map[string]cty.Type,
	steps []string,
	builder *strings.Builder,
) error {
	winner := -1
	for split := 1; split <= len(steps); split++ {
		declared, ok := keyedElementAt(element, attributes, steps[:split])
		if !ok || !consumes(declared, steps[split:]) {
			continue
		}

		if winner >= 0 {
			return nestedValue(strings.Join(steps, "."))
		}

		winner = split
	}

	if winner < 0 {
		return nestedValue(strings.Join(steps, "."))
	}

	key := strings.Join(steps[:winner], ".")
	if hclsyntax.ValidIdentifier(key) {
		builder.WriteString("." + key)
	} else {
		builder.WriteString("[" + strconv.Quote(key) + "]")
	}

	if len(steps[winner:]) == 0 {
		// The key named the whole remainder, and `consumes` proved the element
		// is a scalar leaf before this candidate won.
		return nil
	}

	declared, _ := keyedElementAt(element, attributes, steps[:winner])

	return walkTypeSteps(declared, steps[winner:], builder)
}

// keyedElementAt resolves a candidate key against a keyed collection's
// element type: any key for a map, a named attribute for an object.
func keyedElementAt(
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

// consumes reports whether a type consumes the rest of a payload path down to
// a scalar leaf — the validation half of keyedSteps' candidate enumeration,
// which must not render into the builder it is still deciding for.
func consumes(declared cty.Type, steps []string) bool {
	if len(steps) == 0 {
		return declared.IsPrimitiveType()
	}

	switch {
	case declared.IsListType():
		_, ok := pureIndex(steps[0])

		return ok && consumes(declared.ElementType(), steps[1:])
	case declared.IsTupleType():
		key, ok := pureIndex(steps[0])
		if !ok {
			return false
		}

		position, err := strconv.Atoi(key)

		return err == nil && position < len(declared.TupleElementTypes()) &&
			consumes(declared.TupleElementTypes()[position], steps[1:])
	case declared.IsSetType():
		return false
	case declared.IsMapType():
		return keyedConsumes(declared.ElementType(), nil, steps)
	case declared.IsObjectType():
		return keyedConsumes(cty.NilType, declared.AttributeTypes(), steps)
	default:
		return false
	}
}

func keyedConsumes(element cty.Type, attributes map[string]cty.Type, steps []string) bool {
	winners := 0

	for split := 1; split <= len(steps); split++ {
		declared, ok := keyedElementAt(element, attributes, steps[:split])
		if !ok {
			continue
		}

		if consumes(declared, steps[split:]) {
			winners++
		}
	}

	return winners == 1
}

// indexType resolves a bracketed step against the type it indexes.
func indexType(declared cty.Type, name, key string) (cty.Type, error) {
	switch {
	case declared.IsListType():
		if !digitsOnly(key) {
			return cty.NilType, indexRefusal(declared, name+"["+key+"]")
		}

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

// leafRefusal refuses a path that ends on a typed non-scalar leaf, keeping the
// leafEquality contract: a scalar equality can only express a scalar, or a
// value nothing types.
func leafRefusal(name string, declared cty.Type, known bool) error {
	if known && !declared.IsPrimitiveType() {
		return fmt.Errorf("%w: the provider types %s as %s, which no scalar "+
			"equality can express", ErrUnrenderable, name, declared.FriendlyName())
	}

	return nil
}

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
	return fmt.Errorf("%w: %s is not expressible as a dotted traversal "+
		"(a non-identifier key needs an index form this generator does not emit)",
		ErrUnrenderable, expression)
}

// walkIndicesAndNames renders an output's element path — indices as written,
// further names as dotted attributes — and refuses anything else. A map key
// reaches it only inside an index, never as a bare segment, because no schema
// exists to tell the key from a nested attribute.
func walkIndicesAndNames(attribute string) (string, error) {
	builder := strings.Builder{}

	for _, segment := range fingerprint.AttributeSegments(attribute) {
		if strings.HasPrefix(segment, "[") {
			name, key, indexed := cutIndex(segment)
			if name != "" || !indexed || !digitsOnly(key) {
				return "", nestedValue(attribute)
			}

			builder.WriteString(segment)

			continue
		}

		if !hclsyntax.ValidIdentifier(segment) {
			return "", nestedValue(attribute)
		}

		builder.WriteString(".")
		builder.WriteString(segment)
	}

	return builder.String(), nil
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
	name, key, indexed := cutIndex(segment)

	kind, resourceType, ok := schemaCoordinates(resource)
	if !ok {
		if indexed {
			return "", fmt.Errorf("%w: %s indexes a collection nothing types, and an "+
				"index into a set is not legal Terraform", ErrUnrenderable, segment)
		}

		return scalarEquality(expression, baseline)
	}

	declared, typed := r.schemas.AttributeType(kind, resourceType, name)

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
	case baseline == "null":
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
