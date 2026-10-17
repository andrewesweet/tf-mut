package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// BaselineFormatVersion is the acceptance list's file format contract.
const BaselineFormatVersion = "tf-mut-baseline-1"

// baselineFileMode keeps the acceptance list owner-writable and world-read
// only through the repository's own permissions.
const baselineFileMode = 0o600

// ErrBaselineFile reports a baseline file that could not be read: a corrupt
// acceptance list must never fake an empty one.
var ErrBaselineFile = errors.New("the baseline file could not be read")

// AcceptanceEntry accepts one finding by stable ID and actionability class.
type AcceptanceEntry struct {
	ID string `json:"id"`
	// Actionable is the accepted actionability class: an accepted
	// indeterminate whose diagnosis becomes actionable is new again.
	Actionable bool `json:"actionable"`
	// State, Site and Operator are context for the human reading the file;
	// acceptance is decided by ID and class alone.
	State    string `json:"state,omitempty"`
	Site     string `json:"site,omitempty"`
	Operator string `json:"operator,omitempty"`
}

// baselineFile is the persisted acceptance list.
type baselineFile struct {
	FormatVersion string            `json:"format_version"`
	Entries       []AcceptanceEntry `json:"entries"`
}

// BaselineStore is the acceptance list's persistence boundary. The engine
// owns which findings are accepted — decided only by a full, unsampled,
// freshly executed population — and resolves the path; the store owns the
// file. A missing list is the empty adoption state; a corrupt one is an
// error, never a fake empty list.
type BaselineStore interface {
	// Load reads the accepted entries at path.
	Load(path string) ([]AcceptanceEntry, error)
	// Save writes the accepted entries at path.
	Save(path string, entries []AcceptanceEntry) error
}

// BaselineFile is the filesystem acceptance list.
type BaselineFile struct{}

// NewBaselineFile builds the filesystem baseline store under the shipped
// format version.
func NewBaselineFile() BaselineFile {
	return BaselineFile{}
}

// Load reads the acceptance list at path. A missing file is an empty list —
// that is what adoption looks like — and a corrupt one is an error.
func (BaselineFile) Load(path string) ([]AcceptanceEntry, error) {
	content, err := os.ReadFile(path) //nolint:gosec // module-relative baseline path.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("%w: %s: %w", ErrBaselineFile, path, err)
	}

	decoded := baselineFile{} // decoded from disk.
	if err := json.Unmarshal(content, &decoded); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBaselineFile, path, err)
	}

	if decoded.FormatVersion != BaselineFormatVersion {
		return nil, fmt.Errorf("%w: %s declares format %q, this build reads %q",
			ErrBaselineFile, path, decoded.FormatVersion, BaselineFormatVersion)
	}

	return decoded.Entries, nil
}

// Save persists the accepted list at path, owner-only and indented for the
// human who edits it.
func (BaselineFile) Save(path string, entries []AcceptanceEntry) error {
	encoded, err := json.MarshalIndent(baselineFile{
		FormatVersion: BaselineFormatVersion,
		Entries:       entries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding baseline: %w", err)
	}

	if err := os.WriteFile(path, append(encoded, '\n'), baselineFileMode); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}

	return nil
}
