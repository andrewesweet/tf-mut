package discovery

import (
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The canonical address model (M3a, spec review C3).
//
// One grammar is shared by graph nodes, mutation sites and payload paths, and
// it is the join point between static HCL and the dynamic test_plan/test_state
// JSON:
//
//	address     = { "module." name [ key ] "." } node
//	resource    = type "." name [ key ] [ "." attr-path ]
//	node        = resource | data | named
//	data        = "data." type "." name [ key ] [ "." attr-path ]
//	named       = ( "local" | "var" | "output" ) "." name [ "." attr-path ]
//	attr-path   = segment { "." segment }
//	segment     = name | "*"
//	key         = "[" anything "]"
//
// Instance keys are conservatively wildcarded: `app[0]` and `app["a"]` are the
// same address, in the direction that over-reports matches. Splats and
// wildcards match all segments.

// Wildcard is the match-all segment a splat or an instance key collapses to.
const Wildcard = "*"

// ValidIdentifier exposes HCL's identifier check without requiring consumers
// to import native syntax outside the discovery boundary.
func ValidIdentifier(name string) bool {
	return hclsyntax.ValidIdentifier(name)
}

// Addr is one parsed canonical address.
type Addr struct {
	// ModulePath is the chain of module call names from the root module, with
	// instance keys removed.
	ModulePath []string
	// Parts are the node's segments with instance keys removed and splats
	// rendered as the wildcard.
	Parts []string
}

// ParseAddr parses the canonical grammar. It never fails: an address is a
// sequence of segments whatever its shape, and deciding whether those segments
// name a supported value is each consumer's job, where the decision can fail closed.
func ParseAddr(address string) Addr {
	segments := splitAddress(address)
	modulePath := []string{}

	for len(segments) >= addressParts && segments[0] == moduleBlock {
		modulePath = append(modulePath, segments[1])
		segments = segments[addressParts:]
	}

	return Addr{ModulePath: modulePath, Parts: segments}
}

// BlockAddr renders the canonical address of a configuration block from its
// type and labels. Native HCL and JSON discovery, the graph, mutation generation
// and the oracle share this constructor.
//
// It reports false for unsupported block kinds and incorrect label counts.
// In particular, locals names its attributes and terraform has no block address.
func BlockAddr(kind string, labels []string) (string, bool) {
	switch kind {
	case resourceBlock, dataBlock:
		if len(labels) != resourceLabelCount {
			return "", false
		}

		address := labels[0] + "." + labels[1]
		if kind == dataBlock {
			address = dataBlock + "." + address
		}

		return address, true
	case outputBlock, moduleBlock, checkBlock, providerBlock:
		if len(labels) != 1 {
			return "", false
		}

		return kind + "." + labels[0], true
	case variableBlock:
		if len(labels) != 1 {
			return "", false
		}

		return "var." + labels[0], true
	default:
		return "", false
	}
}

// RootAddr truncates an address to its first two dot-separated segments.
// It reports false when the address has fewer than two segments.
func RootAddr(address string) (string, bool) {
	parts := strings.Split(address, ".")
	if len(parts) < addressParts {
		return "", false
	}

	return parts[0] + "." + parts[1], true
}

// splitAddress splits on dots outside instance keys, dropping the keys and
// keeping splat steps as the wildcard.
func splitAddress(address string) []string {
	segments := []string{}
	builder := strings.Builder{}
	depth := 0

	flush := func() {
		part := builder.String()
		builder.Reset()

		if part == "" {
			return
		}

		segments = append(segments, part)
	}

	for _, letter := range address {
		switch {
		case letter == '[':
			depth++
		case letter == ']':
			depth--
		case depth > 0:
		case letter == '.':
			flush()
		default:
			builder.WriteRune(letter)
		}
	}

	flush()

	return segments
}

// Contains reports whether this address names a value that contains the
// other's, segment by segment, with the wildcard matching anything. A shorter
// address contains its extensions: `terraform_data.app` contains
// `terraform_data.app.input.name`.
func (a Addr) Contains(other Addr) bool {
	if len(a.ModulePath) != len(other.ModulePath) {
		return false
	}

	for index, name := range a.ModulePath {
		if name != other.ModulePath[index] {
			return false
		}
	}

	if len(a.Parts) > len(other.Parts) {
		return false
	}

	for index, segment := range a.Parts {
		if segment != Wildcard && other.Parts[index] != Wildcard && segment != other.Parts[index] {
			return false
		}
	}

	return true
}

// String renders the canonical form.
func (a Addr) String() string {
	rendered := make([]string, 0, len(a.ModulePath)*addressParts+len(a.Parts))

	for _, name := range a.ModulePath {
		address, _ := BlockAddr(moduleBlock, []string{name})
		rendered = append(rendered, address)
	}

	rendered = append(rendered, a.Parts...)

	return strings.Join(rendered, ".")
}
