package suggest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
)

// ErrUnaddressable reports a delta the address adapter cannot express as a
// legal traversal in the selected run's target module.
//
// It is deliberately its own error and its own outcome status. A generated
// traversal that does not compile is a limitation of this generator, and
// reporting it as an ordinary refutation would dress a generator limit as
// verification evidence — the failure shape the M4 spec review's C2 names.
var ErrUnaddressable = errors.New("no legal assertion expression addresses this delta")

// traversalParts is what the address adapter hands the rendering contract:
// the legal expression, and the resource address and attribute path the type
// lookup is keyed by.
type traversalParts struct {
	expression string
	resource   string
	attribute  string
}

// traversal is the assertion-expression adapter: it joins two address spaces
// — the canonical payload path and the HCL an assertion may write — and fails
// closed at every join it cannot make.
//
// The adapter is evaluated per selected run, but takes no run argument today:
// a retargeted run's payload is already expressed relative to its own root,
// so the child-module refusal reads the module path out of the address
// itself. The first feature that needs run-relative name resolution adds the
// parameter back with the need in hand.
func traversal(path string) (traversalParts, error) {
	empty := traversalParts{expression: "", resource: "", attribute: ""}

	resource, attribute, ok := fingerprint.Split(path)
	if !ok {
		return empty, fmt.Errorf(
			"%w: %s names no value a `terraform test` assertion could read", ErrUnaddressable, path,
		)
	}

	if strings.Contains(resource, discovery.Wildcard) || strings.Contains(attribute, discovery.Wildcard) {
		return empty, fmt.Errorf(
			"%w: %s was canonicalised through a splat or wildcard, so the concrete "+
				"instance it names is not recoverable", ErrUnaddressable, path,
		)
	}

	if parsed := discovery.ParseAddr(resource); len(parsed.ModulePath) > 0 {
		return empty, fmt.Errorf(
			"%w: %s is inside %s, and a run rooted at this module can observe a child "+
				"module only through its outputs, never through its internals",
			ErrUnaddressable, resource, "module."+strings.Join(parsed.ModulePath, ".module."),
		)
	}

	expression := resource
	if attribute != "" {
		expression += "." + attribute
	}

	if err := parseTraversal(expression); err != nil {
		// Where the resource address alone is a legal traversal, what failed
		// is the attribute path — a non-identifier map key, most commonly —
		// and that is the rendering contract's refusal (C3), not an
		// addressing one: the value is reachable, it just has no dotted
		// spelling this generator emits.
		if parseTraversal(resource) == nil {
			return empty, fmt.Errorf("%w: %s is not expressible as a dotted traversal "+
				"(a non-identifier key needs an index form this generator does not emit)",
				ErrUnrenderable, expression)
		}

		return empty, err
	}

	return traversalParts{expression: expression, resource: resource, attribute: attribute}, nil
}

// parseTraversal proves the generated text is a traversal HCL accepts.
//
// The check is the adapter's floor: instance keys arrive as Terraform wrote
// them, string keys carry Terraform's own escaping, and rather than trusting
// either the text is handed to the same parser Terraform's own test files go
// through. Anything that is not a pure traversal fails closed.
func parseTraversal(expression string) error {
	parsed, diagnostics := hclsyntax.ParseExpression(
		[]byte(expression), "suggestion", hcl.InitialPos,
	)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s does not parse as an expression: %s",
			ErrUnaddressable, expression, diagnostics.Error())
	}

	if !isTraversal(parsed) {
		return fmt.Errorf("%w: %s is not a plain traversal, so it cannot be trusted "+
			"to name the value the delta is about", ErrUnaddressable, expression)
	}

	return nil
}

// isTraversal reports whether an expression is nothing but a name lookup.
func isTraversal(expr hclsyntax.Expression) bool {
	switch typed := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		return true
	case *hclsyntax.RelativeTraversalExpr:
		return isTraversal(typed.Source)
	case *hclsyntax.IndexExpr:
		if _, literal := typed.Key.(*hclsyntax.LiteralValueExpr); !literal {
			return false
		}

		return isTraversal(typed.Collection)
	default:
		return false
	}
}
