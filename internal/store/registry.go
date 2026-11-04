package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

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

// NewFileRegistry builds the filesystem provenance-registry store.
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
//
// Save is the write half of a read-modify-write, so a caller that merged into
// a registry it read earlier must hold Lock across both halves: the recheck
// proves the file is still a registry, not that it is still the one it read.
func (FileRegistry) Save(path string, registry Registry, recheck func() error) error {
	content, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the provenance registry: %w", err)
	}

	return sandbox.WriteFreshChecked(path, "",
		append(content, '\n'), recheck)
}

// Lock takes the provenance registry's exclusive advisory lock, serialising the
// read-modify-write two invocations committing into one module each perform.
// Callers hold it across the load, the merge and the save; the store's own
// methods take no lock.
//
// The lock is the module directory's, not the registry file's. The registry is
// installed by rename, so lockers that opened the same path moments apart can
// hold locks on two different inodes and run their critical sections at once;
// a directory inode is never replaced, and it adds no file to a module's tree,
// which this tool writes to only through its recorded exceptions.
func (FileRegistry) Lock(path string) (func(), error) {
	directory := filepath.Dir(path)

	file, err := os.Open(directory) //nolint:gosec // a module directory handed to this store.
	if err != nil {
		return nil, fmt.Errorf("opening %s to lock the provenance registry: %w", directory, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("locking %s: %w", directory, err)
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
