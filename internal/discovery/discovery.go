// Package discovery parses a Terraform module and its test suite into the
// inventories the mutation engine needs: configuration blocks, run blocks,
// provider mocking, unsandboxed effects and the local-module closure.
//
// Parsing uses hclsyntax so that every site carries an exact source range.
package discovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ErrParse reports configuration that could not be parsed.
var ErrParse = errors.New("configuration could not be parsed")

// BuiltinProvider is the local name of Terraform's builtin provider, which
// backs terraform_data and terraform_remote_state and never reaches a network.
const BuiltinProvider = "terraform"

// CommandApply and CommandPlan are the two run-block commands.
const (
	CommandApply = "apply"
	CommandPlan  = "plan"
)

// Attribute is a top-level argument assignment inside a block body.
type Attribute struct {
	Name  string
	Range hcl.Range
	Expr  hclsyntax.Expression
}

// Block is a discovered configuration block.
type Block struct {
	// Kind is one of resource, data, output, module or local.
	Kind string
	// Type is the resource or data source type; empty for other kinds.
	Type string
	// Name is the block's second label, or the output/local name.
	Name string
	// Address is the Terraform address of the block within its module.
	Address string
	// File is the absolute path of the file declaring the block.
	File string
	// ModuleRel is the module-relative path of the declaring file.
	ModuleRel string
	// DefRange is the range of the block header.
	DefRange hcl.Range
	// Attributes are the top-level arguments of the block body.
	Attributes []Attribute
	// HasCount and HasForEach record the multiplicity meta-arguments.
	HasCount   bool
	HasForEach bool
	// LocalsIndex identifies which locals block in the file declares a local.
	LocalsIndex int
}

// VariableByName returns the module's variable declaration of that name.
func (m Module) VariableByName(name string) (Block, bool) {
	for _, variable := range m.Variables {
		if variable.Name == name {
			return variable, true
		}
	}

	return Block{}, false
}

// ModuleCall is a module block in a parent module.
type ModuleCall struct {
	Name     string
	Source   string
	Local    bool
	Dir      string
	File     string
	DefRange hcl.Range
	Inputs   []Attribute
	// JSONDeclared marks a call declared in a `.tf.json` file. Its inputs are
	// not decoded into `Inputs` — an `Attribute` carries a native-syntax
	// expression and a JSON one has none — so the reference graph treats the
	// call as unbounded, and no mutation is ever generated on it.
	JSONDeclared bool
}

// Module is one local module in the closure.
type Module struct {
	// Dir is the absolute module directory.
	Dir string
	// Rel is the module directory relative to the closure root.
	Rel string
	// Files lists the absolute paths of the module's .tf files.
	Files []string
	// Bodies holds the parsed body of each module file, keyed by absolute path,
	// so that generation reads the same syntax tree the inventories were built
	// from. Test files are not here: the closure parses those itself, because
	// they are not part of any module.
	Bodies      map[string]*hclsyntax.Body
	Resources   []Block
	DataSources []Block
	Outputs     []Block
	Locals      []Block
	Variables   []Block
	Calls       []ModuleCall
	// Providers are the provider local names the module requires.
	Providers []string
	// ProviderAliases are the provider configurations the module declares.
	ProviderAliases []ProviderAlias
	// Effects are constructs whose apply executes local side effects.
	Effects []Effect
	// References records how each resource address is consumed.
	References map[string][]Reference
	// JSONResources and JSONDataSources are the blocks `.tf.json` files
	// declare. They are kept apart from the HCL inventories because those are
	// the mutation surface and these are not: Tier 0 rewrites through
	// `hclwrite` and Tiers 1–3 rewrite native-syntax byte ranges, so a JSON
	// file is graphed and safety-scanned and never mutated.
	JSONResources   []Block
	JSONDataSources []Block
	// JSONExpansions records what each JSON-declared output or local observes,
	// so the assertion closure can follow a delta through it.
	JSONExpansions map[string][]Ref
	// JSONBodies holds each successfully read JSON configuration file's body,
	// keyed by absolute path, so the reference graph can walk it.
	JSONBodies map[string]hcl.Body
}

// Effect is a construct that is not severed by provider mocking.
type Effect struct {
	// Kind describes the construct, for example provisioner or data.external.
	Kind    string
	Address string
	File    string
	Range   hcl.Range
}

// ReferenceForm classifies how a resource collection is consumed.
type ReferenceForm string

// The reference forms that decide whether emptying an instance set is safe.
const (
	// ReferenceWhole is a reference to the collection itself, such as
	// length(x) or a for expression, which tolerates an empty collection.
	ReferenceWhole ReferenceForm = "whole"
	// ReferenceSplat is a splat expression, which tolerates an empty collection.
	ReferenceSplat ReferenceForm = "splat"
	// ReferenceBare is an attribute access with no instance key, which is
	// statically invalid once the resource has count.
	ReferenceBare ReferenceForm = "bare"
	// ReferenceIndexed is an exact index access, which validates but errors at
	// evaluation time against an empty instance set.
	ReferenceIndexed ReferenceForm = "indexed"
)

// Reference is one consumption of a resource address.
type Reference struct {
	Form  ReferenceForm
	File  string
	Range hcl.Range
}

// Tolerates reports whether the reference form survives an empty instance set.
func (f ReferenceForm) Tolerates() bool {
	return f == ReferenceWhole || f == ReferenceSplat
}

// RunBlock is a run block in a test file.
type RunBlock struct {
	Name string
	// File is the absolute path of the declaring test file.
	File string
	// Rel is the test file path relative to the module directory.
	Rel string
	// Command is plan or apply.
	Command string
	// ModuleSource is the run-block module retarget source, if any.
	ModuleSource string
	// Assertions counts the assert blocks in the run.
	Assertions int
	DefRange   hcl.Range
	// Variables are the run-level variable assignments, part of the
	// conditional-instantiation evaluator's enumerated context (M3a.3).
	Variables []Attribute
	// HasPlanTarget reports a plan_options block with a target, which changes
	// what the run instantiates and fails the evaluator closed.
	HasPlanTarget bool
	// JSONDeclared marks a run block declared in a `.tftest.json` file. Its
	// variables are not decoded into the evaluator's context — an `Attribute`
	// carries a native-syntax expression and a JSON one has none — so the
	// evaluator fails closed on it, and no suggestion may target it.
	JSONDeclared bool
}

// TestSuite is the discovered test inventory.
type TestSuite struct {
	// Dir is the absolute test directory.
	Dir string
	// Files lists the absolute paths of the discovered test files.
	Files []string
	// Runs lists every run block in declaration order per file.
	Runs []RunBlock
	// MockedProviders lists provider local names covered by mock_provider.
	MockedProviders []string
	// Mocks lists every mock_provider block with the configuration it covers.
	Mocks []ProviderAlias
	// References records how test expressions consume resource addresses.
	References map[string][]Reference
	// FileVariables are each test file's file-level variable assignments,
	// keyed by absolute file path (M3a.3 evaluator context).
	FileVariables map[string][]Attribute
	// JSONAssertions lists the addresses JSON-declared assert conditions read.
	// They join the closure imprecisely: a JSON expression reports which
	// addresses it observes and nothing about how.
	JSONAssertions []Assertion
}

// Configuration is everything discovery learned about the module under test.
type Configuration struct {
	// ModuleDir is the absolute root module directory.
	ModuleDir string
	// ClosureRoot is the directory that contains every local module.
	ClosureRoot string
	// Modules is the local module closure, root module first.
	Modules []Module
	// RemoteCalls lists module calls whose source is not a local path.
	RemoteCalls []ModuleCall
	Tests       TestSuite
	// jsonFiles is the closure's JSON-syntax inventory, which the safety floor
	// is decided from. It is unexported so that no caller can construct a
	// Configuration whose floor was never scanned for.
	jsonFiles []JSONFile
}

// ModuleByRel returns the module with the given closure-relative directory.
func (c Configuration) ModuleByRel(rel string) (Module, bool) {
	for _, module := range c.Modules {
		if module.Rel == rel {
			return module, true
		}
	}

	return Module{}, false
}

// Options tune what a discovery reads.
type Options struct {
	// SkipJSON leaves every JSON-syntax file unread, whatever its content.
	//
	// It is a seam control and not a command-line flag, the way the static
	// shortcut control is: it exists so a test can prove the safety floor
	// holds for content the tool has not read, over the same fixtures whose
	// content the reader normally lifts the floor with.
	SkipJSON bool
}

// Discover parses the module at moduleDir together with the test files in
// testDir, which is interpreted relative to the module directory.
func Discover(moduleDir, testDir string) (Configuration, error) {
	return DiscoverWith(moduleDir, testDir, Options{SkipJSON: false})
}

// DiscoverWith is Discover under explicit options.
func DiscoverWith(moduleDir, testDir string, options Options) (Configuration, error) {
	absoluteModuleDir, err := filepath.Abs(moduleDir)
	if err != nil {
		return Configuration{}, fmt.Errorf("resolving module directory: %w", err)
	}

	info, err := os.Stat(absoluteModuleDir)
	if err != nil || !info.IsDir() {
		return Configuration{}, fmt.Errorf("%w: %s is not a directory", ErrParse, moduleDir)
	}

	parser := hclparse.NewParser()

	suite, jsonFiles, err := parseTests(parser, absoluteModuleDir, testDir, options)
	if err != nil {
		return Configuration{}, err
	}

	modules := []Module{}
	remote := []ModuleCall{}
	seen := map[string]bool{}
	queue := []queued{{dir: absoluteModuleDir, key: ""}}

	// Run blocks may retarget another local module, which makes that module
	// part of the closure even when no module call references it.
	for _, run := range suite.Runs {
		if run.ModuleSource == "" || !isLocalSource(run.ModuleSource) {
			continue
		}

		queue = append(queue, queued{
			dir: filepath.Clean(filepath.Join(absoluteModuleDir, run.ModuleSource)),
			key: "test." + run.Name,
		})
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if seen[current.dir] {
			continue
		}

		seen[current.dir] = true

		module, moduleJSON, parseErr := parseModule(parser, current, options)
		if parseErr != nil {
			return Configuration{}, parseErr
		}

		jsonFiles = append(jsonFiles, moduleJSON...)
		modules = append(modules, module)

		for _, call := range module.Calls {
			if !call.Local {
				remote = append(remote, call)

				continue
			}

			queue = append(queue, queued{dir: call.Dir, key: joinKey(current.key, call.Name)})
		}
	}

	closureRoot, err := commonRoot(absoluteModuleDir, modules)
	if err != nil {
		return Configuration{}, err
	}

	for index := range modules {
		rel, relErr := filepath.Rel(closureRoot, modules[index].Dir)
		if relErr != nil {
			return Configuration{}, fmt.Errorf("resolving module path: %w", relErr)
		}

		modules[index].Rel = filepath.ToSlash(rel)
	}

	variableFiles, err := readJSONVariables(absoluteModuleDir, options)
	if err != nil {
		return Configuration{}, err
	}

	return Configuration{
		ModuleDir:   absoluteModuleDir,
		ClosureRoot: closureRoot,
		Modules:     modules,
		RemoteCalls: remote,
		Tests:       suite,
		jsonFiles:   finaliseJSON(closureRoot, append(jsonFiles, variableFiles...)),
	}, nil
}

type queued struct {
	dir string
	key string
}

func joinKey(parent, name string) string {
	if parent == "" {
		return name
	}

	return parent + "." + name
}

func commonRoot(moduleDir string, modules []Module) (string, error) {
	root := moduleDir

	for _, module := range modules {
		root = ancestor(root, module.Dir)
	}

	if root == "" || root == string(filepath.Separator) {
		return "", fmt.Errorf("%w: local module closure escapes to the filesystem root", ErrParse)
	}

	return root, nil
}

func ancestor(left, right string) string {
	leftParts := strings.Split(filepath.Clean(left), string(filepath.Separator))
	rightParts := strings.Split(filepath.Clean(right), string(filepath.Separator))

	shared := []string{}

	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		if leftParts[index] != rightParts[index] {
			break
		}

		shared = append(shared, leftParts[index])
	}

	return strings.Join(shared, string(filepath.Separator))
}

func listFiles(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	files := []string{}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}

		files = append(files, filepath.Join(dir, entry.Name()))
	}

	slices.Sort(files)

	return files, nil
}

func parseFile(parser *hclparse.Parser, path string) (*hclsyntax.Body, error) {
	source, err := os.ReadFile(path) //nolint:gosec // tool-controlled fixture and module paths.
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	file, diagnostics := parser.ParseHCL(source, path)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%w: %s: unexpected body type", ErrParse, path)
	}

	return body, nil
}

// relativePath renders a path relative to a base, for report-facing locations.
func relativePath(base, path string) (string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	return filepath.ToSlash(rel), nil
}
