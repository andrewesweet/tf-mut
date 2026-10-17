package engine_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// F10 (#253): the suite-supplied half of the static Unobservable
// classification. When every run in the selected suite that evaluates the
// module under test assigns a root variable explicitly, the variable's
// declared default is never read, so a VAR-DEFAULT mutant of it is
// Unobservable — decided from the test-file inventory before anything
// executes. Where the claim is not provable from the test files, the mutant
// executes exactly as it always has.

// varInstanceDefault is the variable site every suite-supplied fixture
// mutates; only the child fixture adds a second site for the same name.
const varInstanceDefault = "var.instance.default"

const (
	suiteSetterTestFile      = "tests/good.tftest.hcl"
	builtinDefaultExpression = `"builtin-default"`
	suppliedValueExpression  = `"supplied"`
	nullExpression           = "null"
	defaultChangeOperator    = "VAR-DEFAULT-CHANGE"
	defaultRemoveOperator    = "VAR-DEFAULT-REMOVE"
	defaultNullOperator      = "VAR-DEFAULT-NULL"
	hclTestSyntax            = "hcl"
	unselectedTestSyntax     = "unselected"
	requiredObjectExpression = `{ required = "x" }`
	overrideHCLFile          = "override.tf"
	overrideJSONFile         = "override.tf.json"
)

// defaultMutantsAt returns the VAR-DEFAULT mutants at one variable site.
func defaultMutantsAt(t *testing.T, result report.Report, module string) []report.Mutant {
	t.Helper()

	found := []report.Mutant{}

	for _, mutant := range result.Mutants {
		defaultsVarInstance := mutant.Module == module && mutant.Site == varInstanceDefault
		if defaultsVarInstance && strings.HasPrefix(mutant.Operator, "VAR-DEFAULT-") {
			found = append(found, mutant)
		}
	}

	if len(found) == 0 {
		t.Fatalf("no VAR-DEFAULT mutant at %s in %s; the fixture lost its point", varInstanceDefault, module)
	}

	return found
}

// TestEverySelectedRunSettingTheVariableClassifiesTheDefaultUnobservable is
// the F10 case itself: the setup run retargets another module and both
// consumers set `instance` from the setup run's output, so no run that
// evaluates the module under test can read the declared default.
func TestEverySelectedRunSettingTheVariableClassifiesTheDefaultUnobservable(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State != report.Unobservable {
			t.Errorf("mutant %s (%s): state %s, want %s — every run that evaluates the "+
				"module under test sets instance, so the default is never read",
				mutant.ID, mutant.Operator, mutant.State, report.Unobservable)
		}

		if mutant.ExecutedRuns != 0 {
			t.Errorf("mutant %s (%s): executed %d runs; the classification is static",
				mutant.ID, mutant.Operator, mutant.ExecutedRuns)
		}

		if mutant.Verdict == nil {
			t.Errorf("mutant %s (%s): the verdict must name the variable and the claim",
				mutant.ID, mutant.Operator)

			continue
		}

		if !strings.Contains(mutant.Verdict.Message, "var.instance") {
			t.Errorf("mutant %s (%s): verdict message %q does not name the variable",
				mutant.ID, mutant.Operator, mutant.Verdict.Message)
		}

		if !strings.Contains(mutant.Verdict.Message, "every selected run") {
			t.Errorf("mutant %s (%s): verdict message %q does not state the claim",
				mutant.ID, mutant.Operator, mutant.Verdict.Message)
		}
	}
}

// TestFileLevelVariablesReachEveryRunClassifiesTheDefaultUnobservable: a
// file-level variables block reaches every run in that file, so the claim
// holds for a suite that never mentions the variable inside a run block.
func TestFileLevelVariablesReachEveryRunClassifiesTheDefaultUnobservable(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-filelevel")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
			t.Errorf("mutant %s (%s): state %s after %d runs; the file-level variables block "+
				"supplies every run, so the default is statically unreachable",
				mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}
}

// TestARunOmittingTheVariableKeepsExecution pins the conservative direction:
// one run that evaluates the module under test omits `instance`, so that run
// reads the declared default and the mutants execute as they always have.
func TestARunOmittingTheVariableKeepsExecution(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-omitted")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): classified Unobservable although the omitter reads the default",
				mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; a run the claim cannot prove over must run",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestAChildModuleVariableKeepsExecution pins the non-root direction: the
// child's own default is genuinely never read — the call forwards the supplied
// root variable — but a module call's input is outside the test-file
// inventory, so the child's mutants execute as they always have. The root
// variable itself is classified.
func TestAChildModuleVariableKeepsExecution(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-child")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
			t.Errorf("mutant %s (%s): root state %s after %d runs; every run sets the root variable",
				mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}

	for _, mutant := range defaultMutantsAt(t, result, "child") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): the child variable is not a root-module variable; "+
				"the inventory cannot see the call's input", mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; a variable outside the root module must run",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestTheSelectedSuiteDecidesTheClassification pins the selection contract:
// the rule quantifies over the runs the current filter selects, so the same
// module classifies Unobservable under a selection whose every run sets the
// variable and executes under the selection whose run omits it.
func TestTheSelectedSuiteDecidesTheClassification(t *testing.T) {
	t.Parallel()

	setting := baseConfig(t, copyFixture(t, "suite-supplied-var-selected"))
	setting.TestSelection = []string{suiteSetterTestFile}

	selected, err := engine.Run(t.Context(), setting)
	if err != nil {
		t.Fatalf("selected run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, selected, ".") {
		if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
			t.Errorf("mutant %s (%s): state %s after %d runs under the selection whose "+
				"every run sets the variable", mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}

	omitting := baseConfig(t, copyFixture(t, "suite-supplied-var-selected"))
	omitting.TestSelection = []string{"tests/omitting.tftest.hcl"}

	omitted, err := engine.Run(t.Context(), omitting)
	if err != nil {
		t.Fatalf("omitting run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, omitted, ".") {
		if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): state %s after %d runs under the selection whose run "+
				"omits the variable; it must execute", mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}

	unselected, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-selected")))
	if err != nil {
		t.Fatalf("unselected run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, unselected, ".") {
		if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): state %s after %d runs unselected; the omitter reads the "+
				"default, so the whole suite must execute", mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}
}

// TestAnAutomaticallyLoadedVariableFileKeepsExecution pins the unseen-source
// conservatism: an automatically loaded variable file is a source the
// test-file inventory does not see, so the classification stays silent even
// though every run also sets the variable in the test files.
func TestAnAutomaticallyLoadedVariableFileKeepsExecution(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-tfvars")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): classified Unobservable although an automatically "+
				"loaded variable file is present", mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; an unseen source must keep execution",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestAVariableEnvironmentOverrideKeepsExecution pins the same conservatism
// for TF_VAR_: an environment override is a source the test-file inventory
// does not see, so the classification stays silent.
func TestAVariableEnvironmentOverrideKeepsExecution(t *testing.T) {
	t.Parallel()

	module := copyFixture(t, "suite-supplied-var")

	config := baseConfig(t, module)
	config.Env = append(config.Env, "TF_VAR_instance=ambient")

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): classified Unobservable although TF_VAR_instance is set",
				mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; an environment override must keep execution",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestATargetedRunKeepsExecution pins the plan_options conservatism: the
// run sets the variable and carries a plan_options target, and what survives
// a targeted plan is not modelled — the multiplicity evaluator refuses it the
// same way — so the default keeps today's execution.
func TestATargetedRunKeepsExecution(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-target")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): classified Unobservable although the run carries a plan_options target",
				mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; a targeted run must keep execution",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestAJSONDeclaredRunKeepsExecution pins the JSON conservatism: the suite's
// only run is declared in JSON and assigns the variable through the run's own
// variables block, and the inventory holds no assignment for a JSON-declared
// run — the same conservatism the multiplicity evaluator applies — so the
// default keeps today's execution.
func TestAJSONDeclaredRunKeepsExecution(t *testing.T) {
	t.Parallel()

	result, err := engine.Run(t.Context(), baseConfig(t, copyFixture(t, "suite-supplied-var-json")))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable {
			t.Errorf("mutant %s (%s): classified Unobservable although the declaring run is JSON-declared",
				mutant.ID, mutant.Operator)
		}

		if mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): did not execute; a JSON-declared run must keep execution",
				mutant.ID, mutant.Operator)
		}
	}
}

// TestStaticShortcutsOffExecuteTheSuiteSuppliedDefault: the suite-supplied
// classification is a static shortcut, so disabling the shortcuts executes
// the mutants exactly as before the classification existed.
//
//nolint:paralleltest // owns package-global static-shortcut hook for its lifetime.
func TestStaticShortcutsOffExecuteTheSuiteSuppliedDefault(t *testing.T) {
	module := copyFixture(t, "suite-supplied-var")

	engine.SetStaticShortcutsDisabled(t, module)

	result, err := engine.Run(t.Context(), baseConfig(t, module))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
			t.Errorf("mutant %s (%s): state %s after %d runs with the shortcuts disabled; "+
				"the mutants must execute", mutant.ID, mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}
	}
}

func TestNonNullableSuiteAssignmentsCannotHideDefaultFallback(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		fileValue string
		runValue  string
		nullable  bool
		expected  string
		static    bool
	}{
		{name: "run null", runValue: nullExpression, expected: builtinDefaultExpression},
		{name: "file null", fileValue: nullExpression, expected: builtinDefaultExpression},
		{
			name: "run null overrides file", fileValue: suppliedValueExpression,
			runValue: nullExpression, expected: builtinDefaultExpression,
		},
		{
			name: "run overrides file null", fileValue: nullExpression,
			runValue: suppliedValueExpression, expected: suppliedValueExpression, static: true,
		},
		{name: "run output null", runValue: "run.setup.value", expected: builtinDefaultExpression},
		{name: "nullable run null", runValue: nullExpression, nullable: true, expected: nullExpression, static: true},
		{name: "nullable file null", fileValue: nullExpression, nullable: true, expected: nullExpression, static: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "suite-supplied-var")
			testsupport.WriteFile(t, filepath.Join(module, mainFile), fmt.Sprintf(`
variable "instance" {
  type     = string
  default  = "builtin-default"
  nullable = %t
}
resource "terraform_data" "app" {
  input = var.instance
}
output "echo" {
  value = terraform_data.app.input
}
`, test.nullable))
			testsupport.WriteFile(t, filepath.Join(module, "tests", "setup", mainFile), `
output "value" {
  value = null
}
`)

			fileVariables, runVariables := "", ""
			if test.fileValue != "" {
				fileVariables = "variables { instance = " + test.fileValue + " }"
			}

			if test.runValue != "" {
				runVariables = "variables { instance = " + test.runValue + " }"
			}

			testsupport.WriteFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), fmt.Sprintf(`
%s
run "setup" {
  command = plan
  module {
    source = "./tests/setup"
  }
}
run "consumer" {
  command = plan
  %s
  assert {
    condition     = output.echo == %s
    error_message = "Terraform must use the effective assignment or its non-nullable fallback"
  }
}
`, fileVariables, runVariables, test.expected))

			request := baseConfig(t, module)
			request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator, defaultNullOperator}

			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			assertNullableDefaultVerdicts(t, result, test.static)
		})
	}
}

func assertNullableDefaultVerdicts(t *testing.T, result report.Report, static bool) {
	t.Helper()

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if static {
			if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
				t.Errorf("%s: state %s after %d runs; effective assignment prevents fallback",
					mutant.Operator, mutant.State, mutant.ExecutedRuns)
			}

			continue
		}

		if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
			t.Errorf("%s: state %s after %d runs; null may select the default",
				mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}

		if mutant.Operator == defaultChangeOperator && mutant.State != report.Killed {
			t.Errorf("changed default: state %s, want Killed by the fallback assertion", mutant.State)
		}
	}
}

func TestRetargetedWrappersCannotHideRootDefaultReads(t *testing.T) {
	t.Parallel()

	for _, syntax := range []string{hclTestSyntax, jsonTestSyntax, "transitive", unselectedTestSyntax} {
		t.Run(syntax, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "suite-supplied-var")
			setup := filepath.Join(module, "tests", "setup")
			wrapper := `
module "subject" {
  source = "../.."
}
output "value" {
  value = module.subject.echo
}
`
			if syntax == jsonTestSyntax {
				wrapper = ""
				testsupport.WriteFile(t, filepath.Join(setup, "main.tf.json"), `{
  "module": {"subject": {"source": "../.."}},
  "output": {"value": {"value": "${module.subject.echo}"}}
}`)
			}

			if syntax == "transitive" {
				wrapper = `
module "bridge" {
  source = "./.."
}
output "value" {
  value = module.bridge.value
}
`
				bridge := filepath.Join(module, "tests", "bridge.tf")
				testsupport.WriteFile(t, bridge, `
module "subject" {
  source = "./.."
}
output "value" {
  value = module.subject.echo
}
`)
			}

			testsupport.WriteFile(t, filepath.Join(setup, mainFile), wrapper)
			testsupport.WriteFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), `
run "setup" {
  command = plan
  module {
    source = "./tests/setup"
  }
  variables {
    instance = "wrapper-input-does-not-reach-subject"
  }
  assert {
    condition     = output.value == "builtin-default"
    error_message = "the wrapper's module call reads the root default"
  }
}
`)
			testsupport.WriteFile(t, filepath.Join(module, "tests", "good.tftest.hcl"), `
run "consumer" {
  command = plan
  variables {
    instance = "supplied"
  }
  assert {
    condition     = output.echo == "supplied"
    error_message = "the direct root run uses its assignment"
  }
}
`)

			request := baseConfig(t, module)
			request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator}
			if syntax == unselectedTestSyntax {
				request.TestSelection = []string{suiteSetterTestFile}
			}

			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			assertWrapperDefaultVerdicts(t, result, syntax)
		})
	}
}

func assertWrapperDefaultVerdicts(t *testing.T, result report.Report, syntax string) {
	t.Helper()

	for _, mutant := range defaultMutantsAt(t, result, ".") {
		if syntax == unselectedTestSyntax {
			if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
				t.Errorf("%s: state %s after %d runs; only the direct consumer is selected",
					mutant.Operator, mutant.State, mutant.ExecutedRuns)
			}

			continue
		}

		if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
			t.Errorf("%s: state %s after %d runs; wrapper calls root without an input",
				mutant.Operator, mutant.State, mutant.ExecutedRuns)
		}

		if mutant.Operator == defaultChangeOperator && mutant.State != report.Killed {
			t.Errorf("changed default: state %s, want Killed by the wrapper assertion", mutant.State)
		}
	}
}

func TestSuiteSuppliedDefaultsMustRemainValidDeclarations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		constraint string
		value      string
		valid      bool
	}{
		{name: "required object", constraint: "object({ required = string })", value: requiredObjectExpression},
		{name: "fixed tuple", constraint: "tuple([string])", value: `["x"]`},
		{name: "map", constraint: "map(string)", value: requiredObjectExpression, valid: true},
		{name: "list", constraint: "list(string)", value: `["x"]`, valid: true},
		{
			name: "optional object", constraint: `object({ required = optional(string, "x") })`,
			value: requiredObjectExpression, valid: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "suite-supplied-var-filelevel")
			testsupport.WriteFile(t, filepath.Join(module, mainFile), fmt.Sprintf(`
variable "instance" {
  type    = %s
  default = %s
}
resource "terraform_data" "app" {
  input = var.instance
}
output "echo" {
  value = terraform_data.app.input
}
`, test.constraint, test.value))
			testsupport.WriteFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), fmt.Sprintf(`
run "consumer" {
  command = plan
  variables {
    instance = %s
  }
  assert {
    condition     = output.echo == var.instance
    error_message = "explicit input reaches the output"
  }
}
`, test.value))

			request := baseConfig(t, module)
			request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator, defaultNullOperator}

			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			for _, mutant := range defaultMutantsAt(t, result, ".") {
				if mutant.Operator == defaultChangeOperator && !test.valid {
					if mutant.State != report.Invalid {
						t.Errorf("%s: state %s; Terraform rejects the empty default for %s",
							mutant.Operator, mutant.State, test.constraint)
					}

					continue
				}

				if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
					t.Errorf("%s: state %s after %d runs; valid declaration and explicit input",
						mutant.Operator, mutant.State, mutant.ExecutedRuns)
				}
			}
		})
	}
}

func TestVariableOverridesKeepSuiteSuppliedDefaultsExecuting(t *testing.T) {
	t.Parallel()

	for _, name := range []string{overrideHCLFile, "z_override.tf", overrideJSONFile, "z_override.tf.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := copyFixture(t, "suite-supplied-var-filelevel")
			override := `variable "instance" { nullable = false }`
			if strings.HasSuffix(name, ".json") {
				override = `{"variable":{"instance":{"nullable":false}}}`
			}

			testsupport.WriteFile(t, filepath.Join(module, name), override)
			testsupport.WriteFile(t, filepath.Join(module, "tests", "main.tftest.hcl"), `
variables {
  instance = null
}
run "consumer" {
  command = plan
  assert {
    condition     = output.echo == "builtin-default"
    error_message = "the override makes a null input select the default"
  }
}
`)

			request := baseConfig(t, module)
			request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator, defaultNullOperator}

			result, err := engine.Run(t.Context(), request)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			for _, mutant := range defaultMutantsAt(t, result, ".") {
				if mutant.State == report.Unobservable {
					t.Errorf("%s: Unobservable despite a variable override", mutant.Operator)
				}

				if mutant.Operator == defaultChangeOperator && mutant.State != report.Killed {
					t.Errorf("changed default: state %s, want Killed by the fallback assertion", mutant.State)
				}

				if mutant.Operator == defaultNullOperator && mutant.State != report.Invalid {
					t.Errorf("null default: state %s, want Invalid for the overridden nullable=false", mutant.State)
				}
			}
		})
	}
}

func TestStaticallyDecidedDefaultsCountAsFresh(t *testing.T) {
	t.Parallel()

	for _, noCache := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-cache=%t", noCache), func(t *testing.T) {
			t.Parallel()

			request := baseConfig(t, copyFixture(t, "suite-supplied-var-filelevel"))
			request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator, defaultNullOperator}
			request.NoCache = noCache

			for range 2 {
				result, err := engine.Run(t.Context(), request)
				if err != nil {
					t.Fatalf("run: %v", err)
				}

				if result.Population.Fresh != result.Population.Selected || result.Population.Cached != 0 {
					t.Errorf("population %+v: statically computed verdicts must count as fresh", result.Population)
				}

				for _, mutant := range defaultMutantsAt(t, result, ".") {
					if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
						t.Errorf("%s: state %s after %d runs, want a static verdict",
							mutant.Operator, mutant.State, mutant.ExecutedRuns)
					}
				}
			}
		})
	}
}

//nolint:paralleltest // uses t.Setenv to control the ambient Terraform arguments.
func TestEnvironmentInjectedTestFiltersKeepDefaultMutantsExecuting(t *testing.T) {
	const injected = "TF_CLI_ARGS_test=-filter=tests/omitting.tftest.hcl"
	const cleared = "TF_CLI_ARGS_test="
	const filter = "-filter=tests/omitting.tftest.hcl"

	for _, test := range []struct {
		name    string
		ambient string
		env     []string
		static  bool
	}{
		{name: "configured filter", env: []string{injected}},
		{name: "ambient filter", ambient: filter},
		{name: "configured empty overrides ambient", ambient: filter, env: []string{cleared}, static: true},
		{name: "last configured filter wins", env: []string{cleared, injected}},
		{name: "last configured empty wins", ambient: filter, env: []string{injected, cleared}, static: true},
		{name: "no injected filter", static: true},
	} {
		t.Setenv("TF_CLI_ARGS_test", test.ambient)

		request := baseConfig(t, copyFixture(t, "suite-supplied-var-selected"))
		request.TestSelection = []string{suiteSetterTestFile}
		request.IncludeOperators = []string{defaultChangeOperator, defaultRemoveOperator, defaultNullOperator}
		request.Env = append(request.Env, test.env...)

		result, err := engine.Run(t.Context(), request)
		if err != nil {
			t.Fatalf("%s: run: %v", test.name, err)
		}

		for _, mutant := range defaultMutantsAt(t, result, ".") {
			if test.static {
				if mutant.State != report.Unobservable || mutant.ExecutedRuns != 0 {
					t.Errorf("%s: %s state %s after %d runs; only the setter is selected",
						test.name, mutant.Operator, mutant.State, mutant.ExecutedRuns)
				}

				continue
			}

			if mutant.State == report.Unobservable || mutant.ExecutedRuns == 0 {
				t.Errorf("%s: %s state %s after %d runs; injected filter selects the default reader",
					test.name, mutant.Operator, mutant.State, mutant.ExecutedRuns)
			}

			if mutant.Operator == defaultChangeOperator && mutant.State != report.Killed {
				t.Errorf("%s: changed default state %s, want Killed by the omitter's assertion", test.name, mutant.State)
			}
		}
	}
}
