package engine

import (
	"os"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// M3a.3 (#47): conditional-instantiation NoCoverage, evaluated against the
// mutant. Quoting the C1 disposition: "Pre-classification is per-mutant,
// never per-block: the mutated multiplicity expression must be statically
// zero under every relevant run, and any mutant whose site is in or
// graph-upstream of the multiplicity expression always executes."
//
// The evaluator's context is exactly the enumerated list: run-level
// variables, file-level variables, root and test-scoped variable defaults,
// module retargeting. The supported expression forms are exactly: literals,
// resolved variable references, ?:, boolean and comparison operators, and
// literal arithmetic. Everything else — a plan_options.target, a run.*
// reference, an unsupported form, a TF_VAR_ environment override, a block in
// a child module reached through a module call — fails closed to execution.

// conditionallyUncovered decides per-mutant pre-classification.
func conditionallyUncovered(
	configuration discovery.Configuration,
	graph *discovery.Graph,
	settings Config,
	mutant mutation.Mutant,
) bool {
	rootRel := configuration.RootRelative()
	if mutant.ModuleRel != rootRel {
		// Recorded conservatism: instantiation through a module call would
		// need call-input propagation, which is outside the enumerated
		// context. Child-module blocks fail closed to execution.
		return false
	}

	block, found := resourceBlock(configuration, rootRel, mutant.Resource)
	if !found || (!block.HasCount && !block.HasForEach) {
		return false
	}

	meta := "count"
	if block.HasForEach {
		meta = "for_each"
	}

	// Any mutant in, under, or graph-upstream of the multiplicity expression
	// always executes: this is the run the pre-classification must never
	// suppress.
	if graph.MultiplicityGuard(rootRel, block.Address+"."+meta, mutant.ModuleRel, mutant.Site) {
		return false
	}

	expr, ok := mutatedMultiplicity(mutant, block, meta)
	if !ok {
		return false
	}

	runs := relevantRuns(configuration)
	if len(runs) == 0 {
		return false
	}

	for _, run := range runs {
		if run.HasPlanTarget {
			return false
		}

		value, decided := evaluateMultiplicity(configuration, settings, run, expr)
		if !decided || !isZeroMultiplicity(value, meta) {
			return false
		}
	}

	return true
}

// resourceBlock finds the resource or data block a mutant sits in.
func resourceBlock(
	configuration discovery.Configuration,
	moduleRel, address string,
) (discovery.Block, bool) {
	module, found := configuration.ModuleByRel(moduleRel)
	if !found || address == "" {
		return discovery.Block{}, false
	}

	for _, block := range module.Resources {
		if block.Address == address {
			return block, true
		}
	}

	for _, block := range module.DataSources {
		if block.Address == address {
			return block, true
		}
	}

	return discovery.Block{}, false
}

// mutatedMultiplicity extracts the multiplicity expression from the mutant's
// own rewritten file — the program the claim is about — failing closed when
// the mutant does not parse or the attribute is gone.
func mutatedMultiplicity(
	mutant mutation.Mutant,
	block discovery.Block,
	meta string,
) (hclsyntax.Expression, bool) {
	file, diagnostics := hclparse.NewParser().ParseHCL(mutant.Mutated, mutant.File)
	if diagnostics.HasErrors() {
		return nil, false
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, false
	}

	kind, labels := blockShape(block)

	for _, candidate := range body.Blocks {
		if candidate.Type != kind || len(candidate.Labels) != len(labels) {
			continue
		}

		if !slices.Equal(candidate.Labels, labels) {
			continue
		}

		attribute, found := candidate.Body.Attributes[meta]
		if !found {
			return nil, false
		}

		return attribute.Expr, true
	}

	// The block lives in a different file of the module than the mutated one:
	// its multiplicity expression is untouched by construction, so the
	// discovered attribute is the mutant's own.
	for _, attribute := range block.Attributes {
		if attribute.Name == meta {
			return attribute.Expr, true
		}
	}

	return nil, false
}

func blockShape(block discovery.Block) (string, []string) {
	if block.Kind == "data" {
		return "data", []string{block.Type, block.Name}
	}

	return "resource", []string{block.Type, block.Name}
}

// relevantRuns lists the runs that instantiate the root module: every run
// without a retarget, plus any run retargeting the root itself.
func relevantRuns(configuration discovery.Configuration) []discovery.RunBlock {
	runs := []discovery.RunBlock{}

	for _, run := range configuration.Tests.Runs {
		if run.ModuleSource == "" {
			runs = append(runs, run)

			continue
		}

		if retargetsRoot(configuration, run.ModuleSource) {
			runs = append(runs, run)
		}
	}

	return runs
}

func retargetsRoot(configuration discovery.Configuration, source string) bool {
	if !strings.HasPrefix(source, "./") && !strings.HasPrefix(source, "../") {
		return false
	}

	module, found := configuration.ModuleByRel(configuration.RootRelative())

	return found && module.Dir == configuration.ModuleDir && source == "./."
}

// evaluateMultiplicity evaluates the expression under one run's variable
// context, deciding only when every form is supported and every referenced
// variable resolves.
func evaluateMultiplicity(
	configuration discovery.Configuration,
	settings Config,
	run discovery.RunBlock,
	expr hclsyntax.Expression,
) (cty.Value, bool) {
	names := []string{}
	if !supportedMultiplicityForm(expr, &names) {
		return cty.NilVal, false
	}

	values := map[string]cty.Value{}

	for _, name := range names {
		value, resolved := resolveVariable(configuration, settings, run, name)
		if !resolved {
			return cty.NilVal, false
		}

		values[name] = value
	}

	context := &hcl.EvalContext{
		Variables: map[string]cty.Value{"var": cty.ObjectVal(values)},
		Functions: nil,
	}

	value, diagnostics := expr.Value(context)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsWhollyKnown() {
		return cty.NilVal, false
	}

	return value, true
}

// supportedMultiplicityForm walks the expression, admitting exactly the
// enumerated forms and collecting the variables it references.
func supportedMultiplicityForm(expr hclsyntax.Expression, names *[]string) bool {
	switch typed := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.ScopeTraversalExpr:
		return supportedVariableReference(typed, names)
	case *hclsyntax.ConditionalExpr:
		return supportedMultiplicityForm(typed.Condition, names) &&
			supportedMultiplicityForm(typed.TrueResult, names) &&
			supportedMultiplicityForm(typed.FalseResult, names)
	case *hclsyntax.BinaryOpExpr:
		return supportedOperation(typed.Op) &&
			supportedMultiplicityForm(typed.LHS, names) &&
			supportedMultiplicityForm(typed.RHS, names)
	case *hclsyntax.UnaryOpExpr:
		return supportedMultiplicityForm(typed.Val, names)
	case *hclsyntax.ParenthesesExpr:
		return supportedMultiplicityForm(typed.Expression, names)
	case *hclsyntax.TupleConsExpr:
		for _, element := range typed.Exprs {
			if !supportedMultiplicityForm(element, names) {
				return false
			}
		}

		return true
	case *hclsyntax.ObjectConsExpr:
		for _, item := range typed.Items {
			if !supportedMultiplicityForm(item.KeyExpr, names) ||
				!supportedMultiplicityForm(item.ValueExpr, names) {
				return false
			}
		}

		return true
	case *hclsyntax.ObjectConsKeyExpr:
		return supportedMultiplicityForm(typed.Wrapped, names)
	default:
		return false
	}
}

func supportedVariableReference(expr *hclsyntax.ScopeTraversalExpr, names *[]string) bool {
	traversal := expr.Traversal
	if len(traversal) != 2 || traversal.RootName() != "var" {
		return false
	}

	attribute, ok := traversal[1].(hcl.TraverseAttr)
	if !ok {
		return false
	}

	*names = append(*names, attribute.Name)

	return true
}

// supportedOperation admits boolean, comparison and arithmetic operators.
func supportedOperation(operation *hclsyntax.Operation) bool {
	switch operation {
	case hclsyntax.OpLogicalAnd, hclsyntax.OpLogicalOr,
		hclsyntax.OpEqual, hclsyntax.OpNotEqual,
		hclsyntax.OpLessThan, hclsyntax.OpLessThanOrEqual,
		hclsyntax.OpGreaterThan, hclsyntax.OpGreaterThanOrEqual,
		hclsyntax.OpAdd, hclsyntax.OpSubtract, hclsyntax.OpMultiply,
		hclsyntax.OpDivide, hclsyntax.OpModulo:
		return true
	default:
		return false
	}
}

// resolveVariable resolves var.<name> from the enumerated context: run-level
// variables, then file-level variables, then the root module's default. A
// TF_VAR_ environment override — from the run's environment or the caller's —
// is outside that context and fails the resolution closed.
func resolveVariable(
	configuration discovery.Configuration,
	settings Config,
	run discovery.RunBlock,
	name string,
) (cty.Value, bool) {
	if environmentOverrides(settings, name) {
		return cty.NilVal, false
	}

	// The first level that assigns the name decides. An assignment that is
	// not a context-free literal — a run.* reference, a function — must fail
	// the resolution closed rather than fall through to a weaker level it
	// overrides.
	for _, attributes := range [][]discovery.Attribute{
		run.Variables,
		configuration.Tests.FileVariables[run.File],
	} {
		if expr, assigned := namedAttribute(attributes, name); assigned {
			return literalValue(expr)
		}
	}

	module, found := configuration.ModuleByRel(configuration.RootRelative())
	if found {
		if variable, declared := module.VariableByName(name); declared {
			if expr, assigned := namedAttribute(variable.Attributes, "default"); assigned {
				return literalValue(expr)
			}
		}
	}

	return cty.NilVal, false
}

func environmentOverrides(settings Config, name string) bool {
	key := "TF_VAR_" + name + "="

	for _, entry := range settings.Env {
		if strings.HasPrefix(entry, key) {
			return true
		}
	}

	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, key) {
			return true
		}
	}

	return false
}

// namedAttribute finds an attribute assignment by name.
func namedAttribute(attributes []discovery.Attribute, name string) (hclsyntax.Expression, bool) {
	for _, attribute := range attributes {
		if attribute.Name == name {
			return attribute.Expr, true
		}
	}

	return nil, false
}

// literalValue evaluates an expression as a context-free literal.
func literalValue(expr hclsyntax.Expression) (cty.Value, bool) {
	value, diagnostics := expr.Value(nil)
	if diagnostics.HasErrors() || value.IsNull() || !value.IsWhollyKnown() {
		return cty.NilVal, false
	}

	return value, true
}

// isZeroMultiplicity reports a count of zero or an empty collection.
func isZeroMultiplicity(value cty.Value, meta string) bool {
	if meta == "count" {
		return value.Type() == cty.Number && value.AsBigFloat().Sign() == 0
	}

	if !value.CanIterateElements() {
		return false
	}

	return value.LengthInt() == 0
}

// conditionalNoCoverageVerdict is the finding a pre-classified mutant
// carries.
func conditionalNoCoverageVerdict() *report.Verdict {
	return &report.Verdict{
		Diagnosis: "",
		Message: "the mutated block's multiplicity expression is statically zero under every " +
			"relevant run, so no run instantiates the block and nothing can execute the mutation",
		Fix: "add a run block whose variables make the multiplicity nonzero, or accept that " +
			"the block is untested under the current suite",
		//nolint:exhaustruct // no delta exists: nothing executed.
		Evidence: report.Evidence{ClosureVerdict: "conditional instantiation: statically zero"},
	}
}
