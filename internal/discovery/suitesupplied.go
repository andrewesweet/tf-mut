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
// This inventory proves that the default cannot reach a plan or state. External
// inputs and unsupported run shapes fail the claim closed; the classification
// contract and its guards are documented in docs/design/mutation-operators.md,
// Tier 3, "Suite-supplied defaults".
func (c Configuration) SuiteSuppliedVariable(env, selection []string, name string, site Site) bool {
	root, found := c.ModuleByRel(c.RootRelative())
	if !found {
		return false
	}

	for _, variable := range root.Variables {
		if variable.Name != name {
			continue
		}

		file := strings.TrimSuffix(filepath.Base(variable.File), ".json")
		if file == "override.tf" || strings.HasSuffix(file, "_override.tf") {
			return false
		}
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

	if hasAnyVariableOverride(env) || hasInjectedTestArguments(env) {
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

func hasInjectedTestArguments(env []string) bool {
	arguments := os.Getenv("TF_CLI_ARGS_test")

	for _, entry := range env {
		if value, assigned := strings.CutPrefix(entry, "TF_CLI_ARGS_test="); assigned {
			arguments = value
		}
	}

	return arguments != ""
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
