package suggest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"

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
// the legal expression, the resource address and attribute path the type
// lookup is keyed by, the canonical payload path the evidence is keyed by,
// and that path's parsed value, so the renderer reads the grammar instead of
// re-splitting the attribute strings.
type traversalParts struct {
	expression string
	resource   string
	attribute  string
	path       string
	source     fingerprint.Path
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

	parsed := fingerprint.ParsePath(path)

	resource, attribute, ok := parsed.Split()
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
		moduleAddress := discovery.Addr{ModulePath: parsed.ModulePath}.String()

		return empty, fmt.Errorf(
			"%w: %s is inside %s, and a run rooted at this module can observe a child "+
				"module only through its outputs, never through its internals",
			ErrUnaddressable, resource, moduleAddress,
		)
	}

	expression := resource
	if attribute != "" {
		if attribute[0] == '[' {
			// An element path the output body carries: `outputs.X.value[0].attr`
			// continues from the resource with the index, not a dot.
			expression += attribute
		} else {
			expression += "." + attribute
		}
	}

	// A path that continues past its first segment is rendered by the walk,
	// which owns the parse floor for the expression it builds; the dotted
	// spelling above is only provisional there.
	if strings.HasPrefix(attribute, "[") || len(parsed.Segments()) > 1 ||
		(strings.HasPrefix(resource, "output.") && attribute != "") {
		return traversalParts{
			expression: expression, resource: resource, attribute: attribute,
			path: path, source: parsed,
		}, nil
	}

	if err := parseTraversal(expression); err != nil {
		// Where the resource address alone is a legal traversal, what failed
		// is the attribute path — a non-identifier map key, most commonly —
		// and that is the rendering contract's refusal (C3), not an
		// addressing one: the value is reachable, but this path has no
		// supported traversal spelling.
		if parseTraversal(resource) == nil {
			return empty, fmt.Errorf("%w: %s has no supported traversal spelling",
				ErrUnrenderable, expression)
		}

		return empty, err
	}

	return traversalParts{
		expression: expression, resource: resource, attribute: attribute,
		path: path, source: parsed,
	}, nil
}

// parseTraversal proves the generated text is a traversal HCL accepts.
//
// The check is the adapter's floor: instance keys arrive as Terraform wrote
// them, string keys carry Terraform's own escaping, and rather than trusting
// either the text is handed to the same parser Terraform's own test files go
// through. Anything that is not a pure traversal fails closed.
func parseTraversal(expression string) error {
	parsed, diagnostics := discovery.ParseExpression(
		[]byte(expression), "suggestion", hcl.InitialPos,
	)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s does not parse as an expression: %s",
			ErrUnaddressable, expression, diagnostics.Error())
	}

	if !discovery.IsTraversal(parsed) {
		return fmt.Errorf("%w: %s is not a plain traversal, so it cannot be trusted "+
			"to name the value the delta is about", ErrUnaddressable, expression)
	}

	return nil
}

// splitInstanceIndex reads a resource address that names a counted instance —
// `null_resource.app[1]` — onto the resource and the instance index. An
// address without a numeric trailing index reports false.
func splitInstanceIndex(resource string) (base string, index int, ok bool) {
	open := strings.LastIndex(resource, "[")
	if open < 0 || !strings.HasSuffix(resource, "]") {
		return "", 0, false
	}

	key := resource[open+1 : len(resource)-1]
	if !digitsOnly(key) {
		return "", 0, false
	}

	index, err := strconv.Atoi(key)
	if err != nil {
		return "", 0, false
	}

	return resource[:open], index, true
}
