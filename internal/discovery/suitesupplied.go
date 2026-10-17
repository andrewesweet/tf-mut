package discovery

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// SuiteSuppliedVariable reports whether every selected run that evaluates the
// module under test assigns the named root-module variable explicitly, so its
// declared default is never read.
//
// This is the inventory behind the suite-supplied static Unobservable
// classification (F10, #253): a default nothing reads cannot reach a plan or
// state, and the test files alone decide that. The claim quantifies only over
// the runs the current selection executes, and every source the test files
// cannot see fails it closed:
//
//   - a run that omits the variable could read the default;
//   - a JSON-declared run's variable assignments are not decoded, the same
//     conservatism the multiplicity evaluator holds;
//   - a variable the root module does not declare has no claim here at all —
//     a child module's value comes from its module call's input, which the
//     test files do not carry;
//   - an automatically loaded variable file — terraform.tfvars,
//     terraform.tfvars.json, *.auto.tfvars, *.auto.tfvars.json — is a source
//     the test files do not describe;
//   - any TF_VAR_ environment entry, the caller's ambient or the run's own,
//     is a source the test files do not describe;
//   - a selection that matches no test file, or a module under test no
//     selected run evaluates, leaves nothing to prove the claim over.
func (c Configuration) SuiteSuppliedVariable(env, selection []string, name string, site Site) bool {
	root, found := c.ModuleByRel(c.RootRelative())
	if !found {
		return false
	}

	variable, declared := root.VariableByName(name)
	if !declared || !validVariableDefault(site, name) {
		return false
	}

	nullable := true
	if expr, assigned := namedAttribute(variable.Attributes, "nullable"); assigned {
		value, known := literalValue(expr)
		if !known || value.Type() != cty.Bool {
			return false
		}

		nullable = value.True()
	}

	if hasAnyVariableOverride(env) {
		return false
	}

	if len(autoVariableFiles(c.ModuleDir)) > 0 {
		return false
	}

	runs := c.selectedRunsEvaluatingModuleUnderTest(selection)
	if len(runs) == 0 {
		return false
	}

	for _, run := range runs {
		if run.JSONDeclared {
			return false
		}

		// A plan_options target prunes the graph the run plans, and this
		// version does not model what survives the pruning — the multiplicity
		// evaluator holds the same refusal — so a targeted run cannot carry
		// the claim.
		if run.HasPlanTarget {
			return false
		}

		expr, assigned := namedAttribute(run.Variables, name)
		if !assigned {
			expr, assigned = namedAttribute(c.Tests.FileVariables[run.File], name)
		}

		if !assigned {
			return false
		}

		if !nullable {
			if _, nonNull := literalValue(expr); !nonNull {
				return false
			}
		}
	}

	return true
}

// hasAnyVariableOverride reports whether any TF_VAR_ entry — the caller's
// ambient environment or the run's additions — is present. An override of one
// variable says nothing about another, so any entry at all keeps the claim
// closed: the test files cannot see what the environment supplies.
func hasAnyVariableOverride(env []string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, "TF_VAR_") {
			return true
		}
	}

	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TF_VAR_") {
			return true
		}
	}

	return false
}

func (c Configuration) selectedRunsEvaluatingModuleUnderTest(selection []string) []RunBlock {
	runs := []RunBlock{}

	for _, run := range c.Tests.Runs {
		if len(selection) > 0 && !c.testFileSelected(run, selection) {
			continue
		}

		if run.ModuleSource == "" {
			runs = append(runs, run)

			continue
		}

		if !isLocalSource(run.ModuleSource) {
			continue
		}

		resolved := filepath.Clean(filepath.Join(c.ModuleDir, run.ModuleSource))
		resolvedReal, resolvedErr := filepath.EvalSymlinks(resolved)
		targetReal, targetErr := filepath.EvalSymlinks(c.ModuleDir)
		if resolvedErr != nil || targetErr != nil {
			return nil
		}

		if resolvedReal == targetReal {
			runs = append(runs, run)

			continue
		}

		target, found := c.ModuleByDir(resolved)
		if !found {
			return nil
		}

		exercised := map[string]bool{}
		c.markClosure(target, exercised)

		if exercised[c.RootRelative()] {
			return nil
		}
	}

	return runs
}

func validVariableDefault(site Site, name string) bool {
	file, diagnostics := hclsyntax.ParseConfig(site.Mutated, site.File, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return false
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return false
	}

	for _, block := range body.Blocks {
		if block.Type != "variable" || !slices.Equal(block.Labels, []string{name}) {
			continue
		}

		attribute, present := block.Body.Attributes["default"]
		if !present {
			return true
		}

		value, diagnostics := attribute.Expr.Value(nil)
		if diagnostics.HasErrors() || !value.IsWhollyKnown() {
			return false
		}

		if constraint, present := block.Body.Attributes["type"]; present {
			target, defaults, diagnostics := typeexpr.TypeConstraintWithDefaults(constraint.Expr)
			if diagnostics.HasErrors() {
				return false
			}

			if defaults != nil {
				value = defaults.Apply(value)
			}

			if _, err := convert.Convert(value, target); err != nil {
				return false
			}
		}

		return true
	}

	return false
}

// testFileSelected reports whether a selection entry could match the run's
// declaring test file. Terraform matches a -filter entry against the test
// file's path relative to the module under test — verified against v1.15.8:
// with the suite in a configured test directory, `-filter=tests/main.tftest.hcl`
// selects it and `-filter=main.tftest.hcl` selects nothing. The other
// spellings are admitted so an entry Terraform resolves differently can only
// over-select — which keeps the claim closed — never drop a run the selection
// executes. An entry carrying glob metacharacters matches every file by the
// same rule.
func (c Configuration) testFileSelected(run RunBlock, selection []string) bool {
	candidates := []string{run.Rel, filepath.Base(run.Rel)}

	if relative, err := filepath.Rel(c.Tests.Dir, run.File); err == nil {
		candidates = append(candidates, filepath.ToSlash(relative))
	}

	for _, entry := range selection {
		if strings.ContainsAny(entry, `*?[\`) {
			return true
		}

		if slices.Contains(candidates, entry) {
			return true
		}
	}

	return false
}
