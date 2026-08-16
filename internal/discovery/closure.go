package discovery

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Ref is one address an expression observes, and how precisely.
type Ref struct {
	// Address is the Terraform address, with instance keys removed.
	Address string
	// Precise reports whether reading the referring expression proves that the
	// address's value was read. A splat or a computed index projects a subset,
	// so a match against one is not a proof.
	Precise bool
	// Construct names the form that made the reference imprecise.
	Construct string
}

// Assertion is one address a test assertion reads.
type Assertion struct {
	Ref

	// File is the test file, relative to the module.
	File string
	// Run is the run block name.
	Run string
	// Line is the line the assert block starts on.
	Line int
}

// Location renders the assertion the way a reader would look for it.
func (a Assertion) Location() string {
	return a.File + ":" + a.Run
}

// Closure is the minimal forward closure through outputs and locals.
//
// The M2 spec review (C3) established why address intersection alone cannot
// separate a weak assertion from an absent one: assertions read outputs, deltas
// live in resources, and the dependency passes through outputs and locals. This
// is that closure and no more of the M3 reference graph than that.
type Closure struct {
	// expansions maps an output or local address to what its value observes.
	expansions map[string][]Ref
	// assertions lists every address the suite's assertions read.
	assertions []Assertion
}

// Verdict is what the closure concluded about one delta address.
type Verdict struct {
	// Read reports a proven read by an assertion.
	Read bool
	// Defeated reports that the closure could not decide, because the only
	// candidate reads pass through a projection it cannot follow.
	Defeated bool
	// Assertion is the assertion that read the address, when one did.
	Assertion Assertion
	// Construct names the form that defeated the computation.
	Construct string
}

// BuildClosure computes the forward closure of the module's outputs and locals
// together with the suite's assertion inventory.
func (c Configuration) BuildClosure() Closure {
	closure := Closure{expansions: map[string][]Ref{}, assertions: []Assertion{}}

	for _, module := range c.Modules {
		for _, output := range module.Outputs {
			closure.record("output."+output.Name, output.Attributes, "value")
		}

		for _, local := range module.Locals {
			closure.record("local."+local.Name, local.Attributes, local.Name)
		}
	}

	closure.assertions = c.assertionReads()

	return closure
}

// Reads reports whether any assertion reads the address, directly or through
// the output and local closure.
func (c Closure) Reads(address string) Verdict {
	target := normaliseAddress(address)
	defeated := Verdict{Read: false, Defeated: false, Assertion: Assertion{}, Construct: ""}

	for _, assertion := range c.assertions {
		verdict := c.reach(assertion.Ref, target, map[string]bool{})
		if verdict.Read {
			verdict.Assertion = assertion

			return verdict
		}

		if verdict.Defeated && !defeated.Defeated {
			defeated = verdict
			defeated.Assertion = assertion
		}
	}

	return defeated
}

func (c Closure) record(address string, attributes []Attribute, wanted string) {
	for _, attribute := range attributes {
		if attribute.Name != wanted {
			continue
		}

		c.expansions[address] = append(c.expansions[address], referencesOf(attribute.Expr)...)
	}
}

// reach walks one reference and everything it expands to.
func (c Closure) reach(from Ref, target string, seen map[string]bool) Verdict {
	empty := Verdict{Read: false, Defeated: false, Assertion: Assertion{}, Construct: ""}

	if seen[from.Address] {
		return empty
	}

	seen[from.Address] = true

	if overlaps(from.Address, target) {
		if from.Precise {
			return Verdict{Read: true, Defeated: false, Assertion: Assertion{}, Construct: from.Construct}
		}

		return Verdict{Read: false, Defeated: true, Assertion: Assertion{}, Construct: from.Construct}
	}

	result := empty

	for _, expansion := range c.expansionsFor(from.Address) {
		next := expansion
		if !from.Precise {
			next.Precise = false
			next.Construct = from.Construct
		}

		verdict := c.reach(next, target, seen)
		if verdict.Read {
			return verdict
		}

		if verdict.Defeated {
			result = verdict
		}
	}

	return result
}

// expansionsFor returns what an address's own definition observes, for the
// output and local addresses the closure covers.
func (c Closure) expansionsFor(address string) []Ref {
	if direct, found := c.expansions[address]; found {
		return direct
	}

	// An assertion reading `output.x.y` still depends on `output.x`.
	for candidate, refs := range c.expansions {
		if strings.HasPrefix(address, candidate+".") {
			return refs
		}
	}

	return nil
}

// overlaps reports whether two addresses name values one of which contains the
// other, which is what "an assertion reads this delta" means.
func overlaps(left, right string) bool {
	return left == right ||
		strings.HasPrefix(left, right+".") ||
		strings.HasPrefix(right, left+".")
}

// normaliseAddress removes instance keys so that `app[0].input` and
// `app["a"].input` both match an assertion written against `app`.
//
// The direction is deliberate: instance keys are dropped rather than required,
// so the closure over-reports reads and under-reports "no assertion reads
// this", which is the claim that would be embarrassing to get wrong.
func normaliseAddress(address string) string {
	builder := strings.Builder{}
	depth := 0

	for _, letter := range address {
		switch {
		case letter == '[':
			depth++
		case letter == ']':
			depth--
		case depth == 0:
			builder.WriteRune(letter)
		default:
		}
	}

	return builder.String()
}

// referencesOf lists every address an expression observes.
func referencesOf(expr hclsyntax.Expression) []Ref {
	refs := []Ref{}

	collectRefs(expr, true, "", &refs)

	slices.SortFunc(refs, func(left, right Ref) int {
		return strings.Compare(left.Address, right.Address)
	})

	return refs
}

func collectRefs(expr hclsyntax.Expression, precise bool, construct string, refs *[]Ref) {
	if expr == nil {
		return
	}

	switch typed := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if address, ok := traversalRef(typed.Traversal); ok {
			*refs = append(*refs, Ref{Address: address, Precise: precise, Construct: construct})
		}

		return
	case *hclsyntax.RelativeTraversalExpr:
		collectRefs(typed.Source, precise, construct, refs)

		return
	case *hclsyntax.SplatExpr:
		// A splat projects an attribute of every instance. Matching a delta
		// against its source proves the collection was read, not that the
		// delta's own attribute was: that is exactly the case the honest
		// `unasserted` fallback exists for.
		collectRefs(typed.Source, false, "splat expression", refs)

		return
	case *hclsyntax.ForExpr:
		collectRefs(typed.CollExpr, false, "for expression", refs)
		collectRefs(typed.KeyExpr, false, "for expression", refs)
		collectRefs(typed.ValExpr, false, "for expression", refs)
		collectRefs(typed.CondExpr, false, "for expression", refs)

		return
	case *hclsyntax.IndexExpr:
		if _, literal := typed.Key.(*hclsyntax.LiteralValueExpr); !literal {
			collectRefs(typed.Collection, false, "computed index", refs)
			collectRefs(typed.Key, precise, construct, refs)

			return
		}

		collectRefs(typed.Collection, precise, construct, refs)
		collectRefs(typed.Key, precise, construct, refs)

		return
	}

	for _, child := range childExpressions(expr) {
		collectRefs(child, precise, construct, refs)
	}
}

// childExpressions lists the direct sub-expressions of a node.
func childExpressions(expr hclsyntax.Expression) []hclsyntax.Expression {
	children := []hclsyntax.Expression{}

	_ = hclsyntax.VisitAll(expr, func(node hclsyntax.Node) hcl.Diagnostics {
		nested, ok := node.(hclsyntax.Expression)
		if ok && nested != expr {
			children = append(children, nested)
		}

		return nil
	})

	return children
}

// traversalRef renders a traversal as an address, stopping at the first step
// that is not a static attribute.
func traversalRef(traversal hcl.Traversal) (string, bool) {
	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", false
	}

	parts := []string{root.Name}

	for _, step := range traversal[1:] {
		attribute, ok := step.(hcl.TraverseAttr)
		if !ok {
			break
		}

		parts = append(parts, attribute.Name)
	}

	if len(parts) < addressParts {
		return "", false
	}

	return strings.Join(parts, "."), true
}

// assertionReads parses the suite's assert blocks into the addresses they read.
func (c Configuration) assertionReads() []Assertion {
	parser := hclparse.NewParser()
	reads := []Assertion{}

	for _, path := range c.Tests.Files {
		body, err := parseFile(parser, path)
		if err != nil {
			continue
		}

		relative := path
		if rel, relErr := relativePath(c.ModuleDir, path); relErr == nil {
			relative = rel
		}

		for _, block := range body.Blocks {
			if block.Type != runBlock || len(block.Labels) != 1 {
				continue
			}

			reads = append(reads, assertionsIn(relative, block.Labels[0], block)...)
		}
	}

	return reads
}

func assertionsIn(file, run string, block *hclsyntax.Block) []Assertion {
	reads := []Assertion{}

	for _, nested := range block.Body.Blocks {
		if nested.Type != assertBlock {
			continue
		}

		for _, attribute := range nested.Body.Attributes {
			if attribute.Name != "condition" {
				continue
			}

			for _, ref := range referencesOf(attribute.Expr) {
				reads = append(reads, Assertion{
					Ref:  ref,
					File: file,
					Run:  run,
					Line: nested.DefRange().Start.Line,
				})
			}
		}
	}

	return reads
}
