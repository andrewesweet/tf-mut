package store

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// RegistryFormatVersion is the provenance registry's format version.
const RegistryFormatVersion = "1.0.0"

// Registry records what this tool generated, so that "generated-unmodified",
// "generated-edited" and "pre-existing" are decided mechanically rather than
// guessed at.
type Registry struct {
	Version string `json:"version"`
	// Files maps a module-relative path to what was written there.
	Files map[string]RegistryEntry `json:"files"`
}

// RegistryEntry is one generated file's provenance.
type RegistryEntry struct {
	// Digest is the content digest as written.
	Digest string `json:"digest"`
	// InputDigest is the input closure that made the content green.
	InputDigest string `json:"input_digest"`
	// Pins lists the identifiers of the pins the file carries.
	Pins []string `json:"pins"`
}

// EmptyRegistry is the registry that claims nothing: a file it does not
// name is pre-existing, and a --force over one is refused.
func EmptyRegistry() Registry {
	return Registry{Version: RegistryFormatVersion, Files: map[string]RegistryEntry{}}
}

// FileRegistry is the filesystem provenance registry.
type FileRegistry struct{}

// NewFileRegistry builds the filesystem provenance-registry store. The
// document's format version is the package's declared constant: FileRegistry
// writes it on every save and reads a foreign-version document as empty.
func NewFileRegistry() FileRegistry {
	return FileRegistry{}
}

// Load reads the provenance registry at path, treating anything unreadable as
// an empty one: a registry that cannot be read claims nothing, and claiming
// nothing is the safe direction — it makes --force refuse rather than replace.
func (FileRegistry) Load(path string) Registry {
	empty := EmptyRegistry()

	content, err := os.ReadFile(path) //nolint:gosec // a module-relative tool file.
	if err != nil {
		return empty
	}

	loaded := Registry{Version: "", Files: nil}
	if err := json.Unmarshal(content, &loaded); err != nil || loaded.Version != RegistryFormatVersion {
		return empty
	}

	if loaded.Files == nil {
		loaded.Files = map[string]RegistryEntry{}
	}

	return loaded
}

// Save writes the provenance registry at path. The digest a registry records
// is the written bytes', which is what the caller's collision checks compare
// against. The registry goes through the same rename-window protocol as every
// other file in the target set: recheck runs immediately before the rename,
// so a registry created inside the window by a concurrent invocation — or by
// a user — is never overwritten with no shape check at all.
func (FileRegistry) Save(path string, registry Registry, recheck func() error) error {
	content, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the provenance registry: %w", err)
	}

	return sandbox.WriteFreshChecked(path, "",
		append(content, '\n'), recheck)
}
