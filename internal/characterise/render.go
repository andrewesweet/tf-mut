package characterise

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// Redacted and Executable name the two renderings of one generated file.
//
// They differ in exactly one place — a sensitive variable's assignment — and
// they have to exist separately because the file Terraform plans must carry
// the value while the file a report publishes must not.
const (
	Executable = false
	Redacted   = true
)

// Render produces one generated test file: the mocks, then a run block per
// scenario, carrying the pins of each.
//
// Pins are optional: the harvest scaffold is rendered with none, which is what
// makes it a pure harvest point — an assertion-less run block is legal, passes,
// and still yields the whole mocked state through `-verbose -json`. The same
// renderer then produces the pinned file, so the bytes that were verified and
// the bytes that are written are one sequence.
func Render(
	scaffold Scaffold,
	scenarios []report.Scenario,
	pins []report.Pin,
	redacted bool,
) []byte {
	names := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		names = append(names, scenario.Name)
	}

	builder := strings.Builder{}
	builder.WriteString(GeneratedHeader(scaffold.Options.Version, strings.Join(names, ", ")))

	for _, mock := range scaffold.Mocks {
		builder.WriteString("\n")
		renderMock(&builder, mock)
	}

	for _, scenario := range scenarios {
		values := scaffold.Values[scenario.ID]
		if redacted {
			values = nil
		}

		builder.WriteString("\n")
		renderRun(&builder, scenario, values, pins)
	}

	return []byte(builder.String())
}

func renderMock(builder *strings.Builder, mock Mock) {
	builder.WriteString(`mock_provider "` + mock.Name + `" {`)

	if mock.Alias != "" {
		builder.WriteString("\n  alias = \"" + mock.Alias + "\"\n")
	}

	for _, defaults := range mock.Resources {
		renderDefaults(builder, "mock_resource", defaults)
	}

	for _, defaults := range mock.Data {
		renderDefaults(builder, "mock_data", defaults)
	}

	builder.WriteString("}\n")
}

func renderDefaults(builder *strings.Builder, block string, defaults MockDefaults) {
	names := make([]string, 0, len(defaults.Defaults))
	for name := range defaults.Defaults {
		names = append(names, name)
	}

	slices.Sort(names)

	builder.WriteString("\n  " + block + ` "` + defaults.Type + `" {` + "\n")
	builder.WriteString("    defaults = {\n")

	width := 0
	for _, name := range names {
		width = max(width, len(name))
	}

	for _, name := range names {
		builder.WriteString("      " + pad(name, width) + " = " + defaults.Defaults[name] + "\n")
	}

	builder.WriteString("    }\n  }\n")
}

// renderRun writes one scenario's run block.
//
// The assignments come from the executable map rather than from the scenario's
// reported inputs: a sensitive variable's report carries the withheld marker
// and its run block has to carry the value, because Terraform cannot plan a
// marker.
func renderRun(
	builder *strings.Builder,
	scenario report.Scenario,
	values map[string]string,
	pins []report.Pin,
) {
	builder.WriteString(`run "` + RunPrefix + scenario.Name + `" {` + "\n")
	builder.WriteString("  command   = apply\n")
	builder.WriteString(`  state_key = "` + scenario.StateKey + `"` + "\n")

	if len(scenario.Inputs) > 0 {
		builder.WriteString("\n  variables {\n")

		width := 0
		for _, input := range scenario.Inputs {
			width = max(width, len(input.Name))
		}

		for _, input := range scenario.Inputs {
			assignment := input.Expression
			if executable, found := values[input.Name]; found {
				assignment = executable
			}

			builder.WriteString("    " + pad(input.Name, width) + " = " + assignment + "\n")
		}

		builder.WriteString("  }\n")
	}

	for _, pin := range pins {
		if pin.Status != report.Pinned || pin.Scenario != scenario.ID {
			continue
		}

		builder.WriteString("\n  assert {\n")
		builder.WriteString("    condition     = " + pin.Expression + "\n")
		builder.WriteString(`    error_message = "characterised ` + pin.Address + ` changed"` + "\n")
		builder.WriteString("  }\n")
	}

	builder.WriteString("}\n")
}

// RenderArtefact produces the non-executable artefact: the editable surface
// that carries every open judgement point.
//
// The extension is deliberately one `terraform test` never reads, which is
// what lets three contracts hold at once — a TODO fails loudly, the suite on
// disk is green by construction, and the file an agent edits is the file the
// resume reads.
func RenderArtefact(scaffold Scaffold, scenario report.Scenario, todos []report.Todo) []byte {
	builder := strings.Builder{}
	builder.WriteString(GeneratedHeader(scaffold.Options.Version, scenario.Name))
	builder.WriteString(strings.Join([]string{
		"#",
		"# This file is NOT executable. `terraform test` never reads it.",
		"# Answer each todo below by replacing the placeholder value with one that",
		"# conforms to the constraint quoted beside it,",
		"# then run `tf-mut characterise --resume` to verify and promote it.",
		"",
	}, "\n"))

	for _, todo := range todos {
		builder.WriteString("\ntodo " + `"` + todo.ID + `" {` + "\n")
		builder.WriteString("  variable = \"" + todo.Variable + "\"\n")
		builder.WriteString("  value    = TFMUT_TODO\n")

		if todo.Constraint != "" {
			builder.WriteString("  # constraint: " + oneLine(todo.Constraint) + "\n")
		}

		builder.WriteString("  # declared at: " + todo.Range.File + ":" +
			strconv.Itoa(todo.Range.Start.Line) + "\n")

		if todo.Diagnostic != "" {
			builder.WriteString("  # diagnostic: " + oneLine(todo.Diagnostic) + "\n")
		}

		for _, attempted := range todo.Attempted {
			builder.WriteString("  # attempted: " + oneLine(attempted) + "\n")
		}

		builder.WriteString("}\n")
	}

	return []byte(builder.String())
}

func pad(name string, width int) string {
	return name + strings.Repeat(" ", width-len(name))
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// RenderScaffolds produces the non-executable file the `expect_failures`
// scaffolds live in.
//
// A scaffold names a construct the oracle cannot assert on and the shape of
// the check somebody has to write for it. It is never executable and never
// verified, so it stays outside the suite until that check exists and has been
// proven — which is the whole reason skeleton generation was moved out of a
// milestone that would have shipped it as test content.
func RenderScaffolds(scaffold Scaffold, scaffolds []report.Scaffold) []byte {
	builder := strings.Builder{}
	builder.WriteString(GeneratedHeader(scaffold.Options.Version, "scaffolds"))
	builder.WriteString(strings.Join([]string{
		"#",
		"# This file is NOT executable. `terraform test` never reads it.",
		"# Each scaffold below names a construct no assertion over the plan or the",
		"# state can distinguish. Write the check it proposes, prove it fails for",
		"# the right reason, and only then move it into a test file.",
		"",
	}, "\n"))

	for _, entry := range scaffolds {
		builder.WriteString("\nscaffold " + `"` + entry.ID + `" {` + "\n")
		builder.WriteString("  kind    = \"" + entry.Kind + "\"\n")
		builder.WriteString("  address = \"" + entry.Address + "\"\n")
		builder.WriteString("\n  # Proposed shape:\n")
		builder.WriteString("  #   run \"expect_" + identifierOf(entry.Address) + "\" {\n")
		builder.WriteString("  #     command         = plan\n")
		builder.WriteString("  #     expect_failures = [" + entry.Address + "]\n")
		builder.WriteString("  #   }\n")
		builder.WriteString("}\n")
	}

	return []byte(builder.String())
}

// identifierOf turns an address into something legal as a run block name.
func identifierOf(address string) string {
	return strings.NewReplacer(".", "_", "[", "_", "]", "", `"`, "").Replace(address)
}

// RenderExpectFailures produces the executable file an answered scaffold
// becomes, once its behaviour has been verified.
//
// The answer supplies the inputs that make the construct fail; the tool
// supplies everything else. Nothing here is trusted: the run is executed
// before it is promoted, and a run block asserting a failure that does not
// happen is a failing run block, which is exactly what makes the verification
// worth running.
func RenderExpectFailures(
	scaffold Scaffold,
	entry report.Scaffold,
	checkable string,
	variables map[string]string,
) []byte {
	builder := strings.Builder{}
	builder.WriteString(GeneratedHeader(scaffold.Options.Version, entry.ID))

	for _, mock := range scaffold.Mocks {
		builder.WriteString("\n")
		renderMock(&builder, mock)
	}

	builder.WriteString("\nrun \"" + ExpectPrefix + identifierOf(entry.Address) + "\" {\n")
	builder.WriteString("  command = plan\n")

	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}

	slices.Sort(names)

	if len(names) > 0 {
		width := 0
		for _, name := range names {
			width = max(width, len(name))
		}

		builder.WriteString("\n  variables {\n")

		for _, name := range names {
			builder.WriteString("    " + pad(name, width) + " = " + variables[name] + "\n")
		}

		builder.WriteString("  }\n")
	}

	builder.WriteString("\n  expect_failures = [" + checkable + "]\n}\n")

	return []byte(builder.String())
}

// ExpectPrefix names the generated run block of a promoted scaffold.
const ExpectPrefix = "expect_"

// ScaffoldFile is the module-relative path a promoted scaffold is written to.
func ScaffoldFile(testDirRel, id string) string {
	return path.Join(testDirRel, filePrefix+ExpectPrefix+id+fileSuffix)
}

// Checkable reduces an unassertable construct's site to the object
// `expect_failures` can name: a variable, an output, or the resource whose
// lifecycle block carries the condition.
func Checkable(site string) (string, bool) {
	parts := strings.Split(site, ".")
	if len(parts) < checkableParts {
		return "", false
	}

	return parts[0] + "." + parts[1], true
}

// checkableParts is the length of the shortest address `expect_failures`
// accepts: `var.<name>`, `output.<name>` or `<type>.<name>`.
const checkableParts = 2

// AnsweredVariables reads a scaffold answer — an object expression naming the
// inputs that make the construct fail — into per-variable assignments.
func AnsweredVariables(answer string) (map[string]string, bool) {
	expr, diagnostics := hclsyntax.ParseExpression([]byte(answer), "answer", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, false
	}

	object, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, false
	}

	variables := map[string]string{}

	for _, item := range object.Items {
		name, named := objectKey(item.KeyExpr)
		if !named {
			return nil, false
		}

		value, diagnostics := item.ValueExpr.Value(nil)
		if diagnostics.HasErrors() || !value.IsKnown() {
			return nil, false
		}

		variables[name] = renderValue(value)
	}

	return variables, true
}
