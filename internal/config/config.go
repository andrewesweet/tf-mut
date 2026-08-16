// Package config reads the repository's `.tf-mut.hcl` and the inline
// suppression directives in a module's own source.
//
// Configuration is policy, and policy that quietly disagrees with itself is
// worse than none: duplicate blocks, contradictory include and exclude lists
// and unknown keys are errors rather than last-one-wins.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// FileName is the configuration file, discovered at the target module root and
// nowhere else.
//
// There is no upward search: a mutation run's policy has to be the policy of
// the module being graded, and a file two directories up that silently changed
// a score would be a worse defect than having no configuration at all.
const FileName = ".tf-mut.hcl"

// ErrConfig reports configuration the tool refuses to act on.
var ErrConfig = errors.New("configuration is not usable")

// File is the decoded configuration.
type File struct {
	// Present reports whether a configuration file was found at all.
	Present bool
	// Path is the file's absolute path, when one exists.
	Path string
	// Settings are the run's scalar knobs; a nil member was not configured.
	Settings Settings
	// Operators selects the mutant population.
	Operators Operators
	// Exclude removes sites from the population before execution.
	Exclude Exclude
	// Reporters are the configured outputs, merged additively with the flags.
	Reporters []Reporter
}

// Settings are the scalars the `tf-mut` block carries. Each is a pointer so
// that "not configured" and "configured to the zero value" stay distinct, which
// is what makes per-scalar CLI precedence expressible.
type Settings struct {
	TestDirectory        *string
	Jobs                 *int
	TimeoutFactor        *float64
	MinScore             *float64
	AllowIncompleteScore *bool
}

// Operators selects the population by tier and identifier.
type Operators struct {
	Tier    string
	Include []string
	Exclude []string
}

// Exclude removes sites by path glob and by resource address.
type Exclude struct {
	Paths     []string
	Resources []string
}

// Reporter is one configured output.
type Reporter struct {
	Name string
	Path string
}

// The block types the file may declare.
const (
	blockSettings  = "tf-mut"
	blockOperators = "operators"
	blockExclude   = "exclude"
	blockReporter  = "reporter"
)

// Load reads the configuration at a module root, if there is one.
func Load(moduleDir string) (File, error) {
	path := filepath.Join(moduleDir, FileName)

	content, err := os.ReadFile(path) //nolint:gosec // the module root is the caller's own directory.
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil //nolint:exhaustruct // absence is the zero value.
		}

		return File{}, fmt.Errorf("reading %s: %w", path, err) //nolint:exhaustruct // no file to describe.
	}

	parsed, diagnostics := hclsyntax.ParseConfig(content, path, hcl.InitialPos)
	if diagnostics.HasErrors() {
		//nolint:exhaustruct // no file to describe.
		return File{}, fmt.Errorf("%w: %s: %s", ErrConfig, path, diagnostics.Error())
	}

	body, ok := parsed.Body.(*hclsyntax.Body)
	if !ok {
		return File{}, fmt.Errorf("%w: %s: unexpected body", ErrConfig, path) //nolint:exhaustruct // no file to describe.
	}

	file := File{
		Present:   true,
		Path:      path,
		Settings:  Settings{}, //nolint:exhaustruct // filled from the blocks below.
		Operators: Operators{Tier: "", Include: nil, Exclude: nil},
		Exclude:   Exclude{Paths: nil, Resources: nil},
		Reporters: []Reporter{},
	}

	if err := decodeBlocks(body, &file); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err) //nolint:exhaustruct // no file to describe.
	}

	return file, file.validate()
}

func decodeBlocks(body *hclsyntax.Body, file *File) error {
	seen := map[string]bool{}

	if len(body.Attributes) > 0 {
		return fmt.Errorf("%w: top-level attributes are not configuration; use a block", ErrConfig)
	}

	for _, block := range body.Blocks {
		if block.Type != blockReporter && seen[block.Type] {
			return fmt.Errorf("%w: %s is declared more than once, and the tool will not guess "+
				"which one you meant", ErrConfig, block.Type)
		}

		seen[block.Type] = true

		if err := decodeBlock(block, file); err != nil {
			return err
		}
	}

	return nil
}

func decodeBlock(block *hclsyntax.Block, file *File) error {
	switch block.Type {
	case blockSettings:
		return decodeSettings(block, &file.Settings)
	case blockOperators:
		return decodeOperators(block, &file.Operators)
	case blockExclude:
		return decodeExclude(block, &file.Exclude)
	case blockReporter:
		return decodeReporter(block, file)
	default:
		return fmt.Errorf("%w: unknown block %q", ErrConfig, block.Type)
	}
}

func decodeSettings(block *hclsyntax.Block, settings *Settings) error {
	for name, attribute := range block.Body.Attributes {
		value, err := evaluate(attribute)
		if err != nil {
			return err
		}

		if err := assignSetting(name, value, settings); err != nil {
			return err
		}
	}

	return nil
}

func assignSetting(name string, value cty.Value, settings *Settings) error {
	switch name {
	case "test_dir":
		text, err := asString(name, value)
		settings.TestDirectory = &text

		return err
	case "jobs":
		number, err := asInt(name, value)
		settings.Jobs = &number

		return err
	case "timeout_factor":
		number, err := asFloat(name, value)
		settings.TimeoutFactor = &number

		return err
	case "min_score":
		number, err := asFloat(name, value)
		settings.MinScore = &number

		return err
	case "allow_incomplete_score":
		flag, err := asBool(name, value)
		settings.AllowIncompleteScore = &flag

		return err
	default:
		return fmt.Errorf("%w: unknown setting %q", ErrConfig, name)
	}
}

func decodeOperators(block *hclsyntax.Block, operators *Operators) error {
	for name, attribute := range block.Body.Attributes {
		value, err := evaluate(attribute)
		if err != nil {
			return err
		}

		switch name {
		case "tier":
			operators.Tier, err = asString(name, value)
		case "include":
			operators.Include, err = asStrings(name, value)
		case "exclude":
			operators.Exclude, err = asStrings(name, value)
		default:
			err = fmt.Errorf("%w: unknown operators setting %q", ErrConfig, name)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func decodeExclude(block *hclsyntax.Block, exclude *Exclude) error {
	for name, attribute := range block.Body.Attributes {
		value, err := evaluate(attribute)
		if err != nil {
			return err
		}

		switch name {
		case "paths":
			exclude.Paths, err = asStrings(name, value)
		case "resources":
			exclude.Resources, err = asStrings(name, value)
		default:
			err = fmt.Errorf("%w: unknown exclude setting %q", ErrConfig, name)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func decodeReporter(block *hclsyntax.Block, file *File) error {
	if len(block.Labels) != 1 {
		return fmt.Errorf("%w: a reporter block needs exactly one label", ErrConfig)
	}

	reporter := Reporter{Name: block.Labels[0], Path: ""}

	for name, attribute := range block.Body.Attributes {
		value, err := evaluate(attribute)
		if err != nil {
			return err
		}

		if name != "path" {
			return fmt.Errorf("%w: unknown reporter setting %q", ErrConfig, name)
		}

		if reporter.Path, err = asString(name, value); err != nil {
			return err
		}
	}

	for _, existing := range file.Reporters {
		if existing.Name == reporter.Name {
			return fmt.Errorf("%w: reporter %q is declared more than once", ErrConfig, reporter.Name)
		}
	}

	file.Reporters = append(file.Reporters, reporter)

	return nil
}

// validate rejects a file whose parts contradict each other.
func (f File) validate() error {
	for _, operator := range f.Operators.Include {
		if slices.Contains(f.Operators.Exclude, operator) {
			return fmt.Errorf("%w: operator %s is both included and excluded, and the tool "+
				"will not decide which you meant", ErrConfig, operator)
		}
	}

	return nil
}

func evaluate(attribute *hclsyntax.Attribute) (cty.Value, error) {
	value, diagnostics := attribute.Expr.Value(nil)
	if diagnostics.HasErrors() {
		return cty.NilVal, fmt.Errorf("%w: %s: %s", ErrConfig, attribute.Name, diagnostics.Error())
	}

	return value, nil
}

func asString(name string, value cty.Value) (string, error) {
	if value.Type() != cty.String || value.IsNull() {
		return "", fmt.Errorf("%w: %s must be a string", ErrConfig, name)
	}

	return value.AsString(), nil
}

func asBool(name string, value cty.Value) (bool, error) {
	if value.Type() != cty.Bool || value.IsNull() {
		return false, fmt.Errorf("%w: %s must be true or false", ErrConfig, name)
	}

	return value.True(), nil
}

func asFloat(name string, value cty.Value) (float64, error) {
	if value.Type() != cty.Number || value.IsNull() {
		return 0, fmt.Errorf("%w: %s must be a number", ErrConfig, name)
	}

	number, _ := value.AsBigFloat().Float64()

	return number, nil
}

func asInt(name string, value cty.Value) (int, error) {
	number, err := asFloat(name, value)

	return int(number), err
}

func asStrings(name string, value cty.Value) ([]string, error) {
	if value.IsNull() || !value.CanIterateElements() {
		return nil, fmt.Errorf("%w: %s must be a list of strings", ErrConfig, name)
	}

	values := []string{}

	for _, element := range value.AsValueSlice() {
		if element.Type() != cty.String || element.IsNull() {
			return nil, fmt.Errorf("%w: %s must be a list of strings", ErrConfig, name)
		}

		values = append(values, element.AsString())
	}

	return values, nil
}

// ExcludesPath reports whether a closure-relative file path is excluded.
//
// Matching is `filepath.Match` per segment plus a `**` prefix wildcard, which
// is the shape the design's examples use.
func (e Exclude) ExcludesPath(rel string) bool {
	for _, pattern := range e.Paths {
		if matchPath(pattern, rel) {
			return true
		}
	}

	return false
}

// ExcludesResource reports whether a resource address is excluded.
func (e Exclude) ExcludesResource(address string) bool {
	return address != "" && slices.Contains(e.Resources, address)
}

func matchPath(pattern, rel string) bool {
	if prefix, found := strings.CutSuffix(pattern, "/**"); found {
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}

	if matched, err := filepath.Match(pattern, rel); err == nil && matched {
		return true
	}

	return strings.HasPrefix(rel, strings.TrimSuffix(pattern, "*"))
}
