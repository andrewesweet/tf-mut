package discovery

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// JSONClass names one of Terraform's JSON-syntax file classes.
//
// The classes are kept apart because they inform different things: a
// configuration file can declare a provider or a provisioner, a test file can
// declare a mock or an apply-mode run, and a variables file can only change
// what a static evaluation would have concluded.
type JSONClass string

// The three JSON classes the milestone's safety floor covers.
const (
	// JSONConfiguration is the `.tf.json` configuration class.
	JSONConfiguration JSONClass = "configuration"
	// JSONTest is the `.tftest.json` test class.
	JSONTest JSONClass = "test"
	// JSONVariables is the `terraform.tfvars.json` and `*.auto.tfvars.json`
	// automatically-loaded variables class.
	JSONVariables JSONClass = "variables"
)

// JSONTestSuffix is the test class's file suffix — exported because the apply
// protocol's never-write-JSON rule keys on it, and two spellings of one
// suffix is how a rule gets a side door.
const JSONTestSuffix = ".tftest.json"

// The file suffixes of the other classes.
const (
	jsonConfigurationSuffix = ".tf.json"
	jsonTestSuffix          = JSONTestSuffix
	jsonVariablesSuffix     = ".auto.tfvars.json"
	jsonVariablesFile       = "terraform.tfvars.json"
)

// JSONFile is one Terraform JSON-syntax file found in the closure.
//
// Presence alone is the safety-relevant fact: Terraform reads these files at
// execution time because the sandbox copies the whole closure, so anything the
// tool has not decoded is content it is blind to while Terraform is not.
type JSONFile struct {
	// Path is the absolute file path.
	Path string
	// Rel is the file path relative to the closure root.
	Rel string
	// Class is the Terraform file class the name places it in.
	Class JSONClass
	// Read reports whether discovery decoded the file's content into the
	// inventories. A file that was not read leaves the safety floor down.
	Read bool
	// Reason states why an unread file was not read.
	Reason string
}

// JSONFiles lists every JSON-syntax file in the closure, in path order.
func (c Configuration) JSONFiles() []JSONFile {
	return c.jsonFiles
}

// UnreadJSON lists the JSON-syntax files whose content discovery did not decode.
func (c Configuration) UnreadJSON() []JSONFile {
	unread := []JSONFile{}

	for _, file := range c.jsonFiles {
		if !file.Read {
			unread = append(unread, file)
		}
	}

	return unread
}

// UnreadJSONOfClass lists the unread files of one class.
func (c Configuration) UnreadJSONOfClass(class JSONClass) []JSONFile {
	matching := []JSONFile{}

	for _, file := range c.UnreadJSON() {
		if file.Class == class {
			matching = append(matching, file)
		}
	}

	return matching
}

// unreadFile records a JSON file whose content could not be taken into the
// inventories, with the reason a reader will see in the refusal.
func unreadFile(path string, class JSONClass, err error) JSONFile {
	return JSONFile{Path: path, Rel: path, Class: class, Read: false, Reason: err.Error()}
}

// readFileRecord records a JSON file whose content is in the inventories.
func readFileRecord(path string, class JSONClass) JSONFile {
	return JSONFile{Path: path, Rel: path, Class: class, Read: true, Reason: ""}
}

// finaliseJSON orders the inventory and renders each path relative to the
// closure root, which is the form every refusal quotes.
func finaliseJSON(closureRoot string, files []JSONFile) []JSONFile {
	for index := range files {
		if rel, err := relativePath(closureRoot, files[index].Path); err == nil {
			files[index].Rel = rel
		}
	}

	slices.SortFunc(files, func(left, right JSONFile) int {
		return strings.Compare(left.Path, right.Path)
	})

	return files
}

// listJSON lists a directory's files with the given suffix, treating an absent
// directory as empty: a module with no test directory is not an error.
func listJSON(dir, suffix string) ([]string, error) {
	files, err := listFiles(dir, suffix)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	return files, nil
}

// readJSONVariables decodes every automatically-loaded JSON variables file,
// recording each file's read status.
//
// The values themselves are re-read by the conditional evaluator against
// Terraform's own precedence order; what is decided here is only whether each
// file can be read at all, which is what the floor turns on.
func readJSONVariables(moduleDir string, options Options) ([]JSONFile, error) {
	paths, err := listAutoVariables(moduleDir)
	if err != nil {
		return nil, err
	}

	records := make([]JSONFile, 0, len(paths))

	for _, path := range paths {
		if readErr := skippedOr(options, func() error {
			return readJSONVariableFile(path)
		}); readErr != nil {
			records = append(records, unreadFile(path, JSONVariables, readErr))

			continue
		}

		records = append(records, readFileRecord(path, JSONVariables))
	}

	return records, nil
}

// readJSONVariableFile proves a variables file decodes into constant
// assignments. An expression the evaluator could not resolve to a literal is a
// file it cannot see through, which keeps the floor down for it.
func readJSONVariableFile(path string) error {
	body, err := parseJSONFile(path)
	if err != nil {
		return err
	}

	attributes, diagnostics := body.JustAttributes()
	if diagnostics.HasErrors() {
		return fmt.Errorf("%w: %s: %s", ErrParse, path, diagnostics.Error())
	}

	for name, attribute := range attributes {
		value, valueDiagnostics := attribute.Expr.Value(nil)
		if valueDiagnostics.HasErrors() || !value.IsWhollyKnown() {
			return fmt.Errorf("%w: %s assigns %s from an expression this reader cannot resolve",
				ErrUnmodelledJSON, path, name)
		}
	}

	return nil
}

// listAutoVariables lists the JSON variables files Terraform loads without
// being asked: `terraform.tfvars.json` and every `*.auto.tfvars.json`.
func listAutoVariables(moduleDir string) ([]string, error) {
	files, err := listJSON(moduleDir, jsonVariablesSuffix)
	if err != nil {
		return nil, err
	}

	named, err := listJSON(moduleDir, jsonVariablesFile)
	if err != nil {
		return nil, err
	}

	for _, path := range named {
		if filepath.Base(path) == jsonVariablesFile {
			files = append(files, path)
		}
	}

	return files, nil
}

// ErrSkippedJSON reports a JSON file the seam control left unread.
var ErrSkippedJSON = errors.New("JSON reading is disabled for this discovery")

// skippedOr runs a read unless the options disabled JSON reading.
func skippedOr(options Options, read func() error) error {
	if options.SkipJSON {
		return ErrSkippedJSON
	}

	return read()
}
