package characterise

import (
	"slices"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// Render produces one scenario's generated test file.
//
// Pins are optional: the harvest scaffold is rendered with none, which is what
// makes it a pure harvest point — an assertion-less run block is legal, passes,
// and still yields the whole mocked state through `-verbose -json`. The same
// renderer then produces the pinned file, so the bytes that were verified and
// the bytes that are written are one sequence.
func Render(scaffold Scaffold, scenarios []report.Scenario, pins []report.Pin) []byte {
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
		builder.WriteString("\n")
		renderRun(&builder, scenario, pins)
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

func renderRun(builder *strings.Builder, scenario report.Scenario, pins []report.Pin) {
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
			builder.WriteString("    " + pad(input.Name, width) + " = " + input.Expression + "\n")
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
