package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ErrNoTests reports a test directory that declares no run blocks.
var ErrNoTests = errors.New("no run blocks found")

const (
	testFileSuffix = ".tftest.hcl"
	runBlock       = "run"
	assertBlock    = "assert"
	mockProvider   = "mock_provider"
	providerBlock  = "provider"
	variablesBlock = "variables"
	planOptions    = "plan_options"
)

func parseTests(
	parser *hclparse.Parser,
	moduleDir, testDir string,
	options Options,
) (TestSuite, []JSONFile, error) {
	absoluteTestDir := testDir
	if !filepath.IsAbs(absoluteTestDir) {
		absoluteTestDir = filepath.Join(moduleDir, testDir)
	}

	suite := TestSuite{
		Dir:             absoluteTestDir,
		Files:           []string{},
		Runs:            []RunBlock{},
		MockedProviders: []string{},
		Mocks:           []ProviderAlias{},
		References:      map[string][]Reference{},
		FileVariables:   map[string][]Attribute{},
		JSONAssertions:  []Assertion{},
	}

	files, err := listTestFiles(moduleDir, absoluteTestDir, testFileSuffix)
	if err != nil {
		return TestSuite{}, nil, err
	}

	suite.Files = files
	mocked := map[string]bool{}

	for _, path := range files {
		body, parseErr := parseFile(parser, path)
		if parseErr != nil {
			return TestSuite{}, nil, parseErr
		}

		relative, relErr := filepath.Rel(moduleDir, path)
		if relErr != nil {
			return TestSuite{}, nil, fmt.Errorf("resolving test file path: %w", relErr)
		}

		collectTestFile(&suite, mocked, path, filepath.ToSlash(relative), body, parser.Sources()[path])
		collectTestReferences(&suite, path, body)
	}

	jsonFiles, err := readJSONTests(&suite, mocked, moduleDir, absoluteTestDir, options)
	if err != nil {
		return TestSuite{}, nil, err
	}

	suite.MockedProviders = sortedKeys(mocked)

	return suite, jsonFiles, nil
}

// listTestFiles lists the test files of one syntax, from the test directory and
// from the module root, the way Terraform reads them.
func listTestFiles(moduleDir, testDir, suffix string) ([]string, error) {
	files, listErr := listFiles(testDir, suffix)
	if listErr != nil {
		if !errors.Is(listErr, fs.ErrNotExist) {
			return nil, listErr
		}

		files = nil
	}

	rootFiles, err := listFiles(moduleDir, suffix)
	if err != nil {
		return nil, err
	}

	files = append(files, rootFiles...)
	slices.Sort(files)

	return files, nil
}

// readJSONTests decodes every `.tftest.json` file, recording each file's read
// status. A file that cannot be read contributes nothing.
func readJSONTests(
	suite *TestSuite,
	mocked map[string]bool,
	moduleDir, testDir string,
	options Options,
) ([]JSONFile, error) {
	paths, err := listTestFiles(moduleDir, testDir, jsonTestSuffix)
	if err != nil {
		return nil, err
	}

	records := make([]JSONFile, 0, len(paths))

	for _, path := range paths {
		scratch := TestSuite{ //nolint:exhaustruct // a decode sink: only the collected fields matter.
			References:     map[string][]Reference{},
			JSONAssertions: []Assertion{},
		}
		found := map[string]bool{}

		if readErr := skippedOr(options, func() error {
			return readJSONTest(&scratch, found, moduleDir, path)
		}); readErr != nil {
			records = append(records, unreadFile(path, JSONTest, readErr))

			continue
		}

		mergeJSONTests(suite, mocked, scratch, found)
		records = append(records, readFileRecord(path, JSONTest))
	}

	return records, nil
}

func mergeJSONTests(suite *TestSuite, mocked map[string]bool, scratch TestSuite, found map[string]bool) {
	suite.Runs = append(suite.Runs, scratch.Runs...)
	suite.Mocks = append(suite.Mocks, scratch.Mocks...)
	suite.JSONAssertions = append(suite.JSONAssertions, scratch.JSONAssertions...)

	for address, references := range scratch.References {
		suite.References[address] = append(suite.References[address], references...)
	}

	for name := range found {
		mocked[name] = true
	}
}

func collectTestFile(
	suite *TestSuite,
	mocked map[string]bool,
	path, relative string,
	body *hclsyntax.Body,
	source []byte,
) {
	for _, block := range body.Blocks {
		switch block.Type {
		case mockProvider:
			if len(block.Labels) == 1 {
				mocked[block.Labels[0]] = true
				suite.Mocks = append(suite.Mocks, mockedConfiguration(block))
			}
		case runBlock:
			if len(block.Labels) == 1 {
				suite.Runs = append(suite.Runs, newRun(path, relative, block, source))
			}
		case variablesBlock:
			suite.FileVariables[path] = append(suite.FileVariables[path], attributesOf(block.Body)...)
		default:
		}
	}
}

// mockedConfiguration records which provider configuration a mock covers.
func mockedConfiguration(block *hclsyntax.Block) ProviderAlias {
	covered := ProviderAlias{Name: block.Labels[0], Alias: ""}

	if attribute, found := block.Body.Attributes["alias"]; found {
		covered.Alias = literalString(attribute.Expr)
	}

	return covered
}

func newRun(path, relative string, block *hclsyntax.Block, source []byte) RunBlock {
	run := RunBlock{
		Name:     block.Labels[0],
		File:     path,
		Rel:      relative,
		Command:  CommandApply,
		DefRange: block.DefRange(),
	}

	if attribute, found := block.Body.Attributes["command"]; found {
		if name := traversalRootName(attribute.Expr); name != "" {
			run.Command = name
		}
	}

	for _, nested := range block.Body.Blocks {
		switch nested.Type {
		case assertBlock:
			run.Assertions++
			run.Asserts = append(run.Asserts, assertBlockOf(nested, source))
		case moduleBlock:
			if source, found := nested.Body.Attributes["source"]; found {
				run.ModuleSource = literalString(source.Expr)
			}
		case variablesBlock:
			run.Variables = append(run.Variables, attributesOf(nested.Body)...)
		case planOptions:
			if _, found := nested.Body.Attributes["target"]; found {
				run.HasPlanTarget = true
			}
		default:
		}
	}

	return run
}

// assertBlockOf records one assertion's condition range and verbatim source.
func assertBlockOf(block *hclsyntax.Block, source []byte) AssertBlock {
	condition, found := block.Body.Attributes["condition"]
	if !found {
		return AssertBlock{Range: block.DefRange(), Source: ""}
	}

	span := condition.Expr.Range()
	text := ""

	if span.Start.Byte >= 0 && span.End.Byte <= len(source) && span.Start.Byte < span.End.Byte {
		text = strings.TrimSpace(string(source[span.Start.Byte:span.End.Byte]))
	}

	return AssertBlock{Range: span, Source: text}
}

// collectTestReferences records resource consumption inside test files, which
// counts towards the multiplicity gate exactly as module references do.
func collectTestReferences(suite *TestSuite, path string, body *hclsyntax.Body) {
	holder := Module{References: suite.References}
	WalkExpressions(body, func(expr hclsyntax.Expression) {
		recordReference(&holder, path, expr)
	})
}

// traversalRootName returns the leading identifier of a bare keyword
// expression such as the plan or apply given to a run block's command.
func traversalRootName(expr hclsyntax.Expression) string {
	traversal, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(traversal.Traversal) == 0 {
		return ""
	}

	return traversal.Traversal.RootName()
}

// HasApplyRun reports whether any run block executes in apply mode.
func (s TestSuite) HasApplyRun() bool {
	for _, run := range s.Runs {
		if run.Command == CommandApply {
			return true
		}
	}

	return false
}

// UnmockedProviders lists providers a run would reach without mocking.
//
// The builtin provider is never included: it reaches no network and no
// credentials.
func (c Configuration) UnmockedProviders() []string {
	mocked := map[string]bool{}
	for _, name := range c.Tests.MockedProviders {
		mocked[name] = true
	}

	unmocked := map[string]bool{}

	for _, module := range c.Modules {
		for _, provider := range module.Providers {
			if provider == BuiltinProvider || mocked[provider] {
				continue
			}

			unmocked[provider] = true
		}
	}

	return sortedKeys(unmocked)
}

// Effects lists every unsandboxed effect across the local module closure.
func (c Configuration) Effects() []Effect {
	effects := []Effect{}
	for _, module := range c.Modules {
		effects = append(effects, module.Effects...)
	}

	slices.SortFunc(effects, func(left, right Effect) int {
		if left.File != right.File {
			return strings.Compare(left.File, right.File)
		}

		return left.Range.Start.Byte - right.Range.Start.Byte
	})

	return effects
}

// ExercisedModules returns the closure-relative directories of every module a
// run block instantiates, including the local modules each of them calls.
func (c Configuration) ExercisedModules() map[string]bool {
	exercised := map[string]bool{}

	root, found := c.ModuleByRel(c.RootRelative())
	if !found {
		return exercised
	}

	for _, run := range c.Tests.Runs {
		target := root

		if run.ModuleSource != "" {
			dir := filepath.Clean(filepath.Join(c.ModuleDir, run.ModuleSource))
			if !isLocalSource(run.ModuleSource) {
				continue
			}

			candidate, ok := c.ModuleByDir(dir)
			if !ok {
				continue
			}

			target = candidate
		}

		c.markClosure(target, exercised)
	}

	return exercised
}

func (c Configuration) markClosure(module Module, exercised map[string]bool) {
	if exercised[module.Rel] {
		return
	}

	exercised[module.Rel] = true

	for _, call := range module.Calls {
		if !call.Local {
			continue
		}

		child, found := c.ModuleByDir(call.Dir)
		if !found {
			continue
		}

		c.markClosure(child, exercised)
	}
}

// ModuleByDir returns the local module rooted at the given absolute directory.
func (c Configuration) ModuleByDir(dir string) (Module, bool) {
	for _, module := range c.Modules {
		if module.Dir == dir {
			return module, true
		}
	}

	return Module{}, false
}

// RootRelative is the root module directory relative to the closure root.
func (c Configuration) RootRelative() string {
	rel, err := filepath.Rel(c.ClosureRoot, c.ModuleDir)
	if err != nil {
		return "."
	}

	return filepath.ToSlash(rel)
}

// TestDirRelative is the test directory relative to the root module directory.
func (c Configuration) TestDirRelative() string {
	rel, err := filepath.Rel(c.ModuleDir, c.Tests.Dir)
	if err != nil {
		return "tests"
	}

	return filepath.ToSlash(rel)
}

// LockFilePath returns the module's dependency lock file path, if present.
func (c Configuration) LockFilePath() (string, bool) {
	path := filepath.Join(c.ModuleDir, ".terraform.lock.hcl")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}

	return path, true
}

// MocksIn reads the provider configurations a generated test file's
// `mock_provider` blocks actually cover.
//
// It parses rendered bytes rather than trusting a plan that produced them,
// which is what the staged provider gate needs: comparing the configurations
// discovery found against the configurations something *intended* to mock is
// comparing a set with itself, and can never separate them. Comparing them
// against what the renderer emitted can.
func MocksIn(source []byte) ([]ProviderAlias, error) {
	file, diagnostics := hclsyntax.ParseConfig(source, "generated", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: generated: %s", ErrParse, diagnostics.Error())
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%w: generated: unexpected body type", ErrParse)
	}

	mocks := []ProviderAlias{}

	for _, block := range body.Blocks {
		if block.Type != mockProvider || len(block.Labels) != 1 {
			continue
		}

		mocks = append(mocks, mockedConfiguration(block))
	}

	return mocks, nil
}
