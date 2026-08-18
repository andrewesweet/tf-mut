package suggest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

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

	segments := attributeSegments(attribute)

	switch {
	case attribute == "":
		// A whole output value. No provider schema types an output, so the
		// scalar in front of us is the only type evidence there is.
		return scalarEquality(expression, baseline)
	case len(segments) > 1:
		return "", fmt.Errorf("%w: %s is a nested value, and the payload path alone "+
			"cannot tell a map key from an object attribute", ErrUnrenderable, attribute)
	default:
		return r.leafEquality(expression, resource, segments[0], baseline)
	}
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
		string(hclwrite.TokensForValue(value).Bytes())), nil
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

// attributeSegments splits an attribute path on its top-level dots, keeping
// element keys attached to the segment they index.
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
