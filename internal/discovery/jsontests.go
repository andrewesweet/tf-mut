package discovery

import (
	"fmt"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
)

// jsonTestSchema is the top-level block set of a `.tftest.json` file.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonTestSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: runBlock, LabelNames: []string{nameLabel}},
		{Type: mockProvider, LabelNames: []string{nameLabel}},
		{Type: providerBlock, LabelNames: []string{nameLabel}},
		{Type: variablesBlock, LabelNames: nil},
	},
}

// jsonRunSchema is the nested block set of a JSON `run` block.
//
// `expect_failures` and the override blocks are deliberately absent: each
// changes what an execution outcome means or what a run actually evaluates,
// and this version does not model either, so their presence must leave the
// file unread rather than be silently accepted.
//
//nolint:gochecknoglobals // an immutable schema.
var jsonRunSchema = &hcl.BodySchema{
	Attributes: nil,
	Blocks: []hcl.BlockHeaderSchema{
		{Type: assertBlock, LabelNames: nil},
		{Type: moduleBlock, LabelNames: nil},
		{Type: variablesBlock, LabelNames: nil},
		{Type: planOptions, LabelNames: nil},
	},
}

// readJSONTest decodes one `.tftest.json` file into the test inventory.
//
// Run blocks are read because the mock inventory and the apply-mode question
// are both decided from them, and both feed a safety gate. What is deliberately
// not read is any route to writing the file back: the JSON test writer is not
// built, so a suggestion whose target run lives here is reported unsupported.
func readJSONTest(suite *TestSuite, mocked map[string]bool, moduleDir, path string) error {
	body, err := parseJSONFile(path)
	if err != nil {
		return err
	}

	content, rest, diagnostics := body.PartialContent(jsonTestSchema)
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	if unmodelledErr := refuseUnmodelled(path, rest); unmodelledErr != nil {
		return unmodelledErr
	}

	relative, err := filepath.Rel(moduleDir, path)
	if err != nil {
		return fmt.Errorf("resolving test file path: %w", err)
	}

	relative = filepath.ToSlash(relative)

	for _, block := range content.Blocks {
		switch block.Type {
		case mockProvider:
			mocked[block.Labels[0]] = true
			suite.Mocks = append(suite.Mocks, jsonMockedConfiguration(block))
		case runBlock:
			run, err := jsonRun(path, relative, block)
			if err != nil {
				return err
			}

			suite.Runs = append(suite.Runs, run)
			suite.JSONAssertions = append(suite.JSONAssertions,
				jsonAssertions(relative, block)...)
		default:
		}

		if err := collectJSONTestReferences(suite, path, block.Body); err != nil {
			return err
		}
	}

	return nil
}

func jsonMockedConfiguration(block *hcl.Block) ProviderAlias {
	covered := ProviderAlias{Name: block.Labels[0], Alias: ""}

	attributes, diagnostics := block.Body.JustAttributes()
	if diagnostics.HasErrors() {
		return covered
	}

	if attribute, found := attributes["alias"]; found {
		covered.Alias = jsonLiteralString(attribute.Expr)
	}

	return covered
}

func jsonRun(path, relative string, block *hcl.Block) (RunBlock, error) {
	run := RunBlock{
		Name: block.Labels[0], File: path, Rel: relative, Command: CommandApply,
		ModuleSource: "", Assertions: 0, DefRange: block.DefRange,
		Variables: nil, HasPlanTarget: false, JSONDeclared: true,
	}

	nested, rest, diagnostics := block.Body.PartialContent(jsonRunSchema)
	if diagnostics.HasErrors() {
		return RunBlock{}, fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	attributes, diagnostics := rest.JustAttributes()
	if diagnostics.HasErrors() {
		return RunBlock{}, fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	// The only run argument this version models is `command`. Everything else
	// a run can carry — `providers` remaps which provider configuration a run
	// sees, `expect_failures` changes what a failing evaluation means — can
	// affect execution in ways the inventories would not reflect, so any other
	// attribute leaves the file unread and the floor down.
	for name := range attributes {
		if name != "command" {
			return RunBlock{}, fmt.Errorf("%w: %s run %q declares %s, which this version "+
				"does not model", ErrUnmodelledJSON, path, block.Labels[0], name)
		}
	}

	if attribute, found := attributes["command"]; found {
		if name := jsonLiteralString(attribute.Expr); name != "" {
			run.Command = name
		}
	}

	for _, inner := range nested.Blocks {
		switch inner.Type {
		case assertBlock:
			run.Assertions++
		case moduleBlock:
			run.ModuleSource = jsonAttributeString(inner.Body, "source")
		case planOptions:
			if jsonHasAttribute(inner.Body, "target") {
				run.HasPlanTarget = true
			}
		default:
		}
	}

	return run, nil
}

// jsonAssertions lists the addresses a JSON run's assert conditions read.
func jsonAssertions(relative string, block *hcl.Block) []Assertion {
	nested, _, diagnostics := block.Body.PartialContent(jsonRunSchema)
	if diagnostics.HasErrors() {
		return nil
	}

	reads := []Assertion{}

	for _, inner := range nested.Blocks {
		if inner.Type != assertBlock {
			continue
		}

		attributes, attributeDiagnostics := inner.Body.JustAttributes()
		if attributeDiagnostics.HasErrors() {
			continue
		}

		condition, found := attributes["condition"]
		if !found {
			continue
		}

		for _, ref := range jsonRefs(condition.Expr) {
			reads = append(reads, Assertion{
				Ref:  ref,
				File: relative,
				Run:  block.Labels[0],
				Line: inner.DefRange.Start.Line,
			})
		}
	}

	return reads
}

func collectJSONTestReferences(suite *TestSuite, path string, body hcl.Body) error {
	holder := Module{References: suite.References} //nolint:exhaustruct // a reference sink only.

	return collectJSONReferences(&holder, path, body)
}

func jsonAttributeString(body hcl.Body, name string) string {
	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return ""
	}

	attribute, found := attributes[name]
	if !found {
		return ""
	}

	return jsonLiteralString(attribute.Expr)
}

func jsonHasAttribute(body hcl.Body, name string) bool {
	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return false
	}

	_, found := attributes[name]

	return found
}
