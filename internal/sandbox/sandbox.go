// Package sandbox materialises the isolated working directories mutants
// execute in.
//
// The contract it enforces is the reason the tool needs no git dependency and
// cannot leave drift behind: the source tree is only ever read, mutated files
// are always written as fresh inodes, and everything expensive — the provider
// tree and remote module payloads — is shared read-only from one warm
// workspace.
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrSharedInode reports a mutated write that would have reached the source
// tree through a shared inode. It is the R2-3 reproduction guard.
var ErrSharedInode = errors.New("refusing to write a mutated file that shares an inode with the source tree")

// DataDirName is Terraform's per-working-directory data directory.
const DataDirName = ".terraform"

// LockFileName is Terraform's dependency lock file.
const LockFileName = ".terraform.lock.hcl"

// CacheDirName is tf-mut's own project-local verdict cache, which must never
// be copied into a sandbox.
const CacheDirName = ".tf-mut-cache"

const (
	modulesDirName  = "modules"
	modulesFileName = "modules.json"
	providersDir    = "providers"

	directoryMode = 0o750
	fileMode      = 0o600
)

// skippedDirs are never copied into a sandbox.
//
//nolint:gochecknoglobals // an immutable lookup table.
var skippedDirs = map[string]bool{
	DataDirName:  true,
	".git":       true,
	CacheDirName: true,
}

// linkableSuffixes are the file kinds Terraform reads and never writes, and so
// the only ones a sandbox may share by hardlink. Everything else — the
// dependency lock file above all — is copied, because a hardlink shares the
// source inode and any in-place write would reach the source tree (R2-3).
//
//nolint:gochecknoglobals // an immutable lookup table.
var linkableSuffixes = []string{
	".tf",
	".tf.json",
	".tftest.hcl",
	".tftest.json",
	".tfmock.hcl",
	".tfmock.json",
	".tfvars",
	".tfvars.json",
}

// Share describes the warm workspace a sandbox borrows from.
type Share struct {
	// DataDir is the warm module's .terraform directory.
	DataDir string
	// LockFile is the warm module's dependency lock file, when one exists.
	LockFile string
}

// Spec describes one sandbox to materialise.
type Spec struct {
	// SourceRoot is the closure root of the module under test. Read only.
	SourceRoot string
	// ModuleRel is the module directory relative to SourceRoot.
	ModuleRel string
	// Target is the directory to create the sandbox in. It must not exist.
	Target string
	// Mutations maps closure-relative paths to their mutated content.
	Mutations map[string][]byte
	// Staged maps closure-relative paths to content the sandbox materialises
	// whether or not the source tree declares the file. It is the staged-suite
	// overlay: a generated test exists in the sandbox and in no source tree,
	// which is what lets a characterisation converge without writing a byte.
	Staged map[string][]byte
	// Share borrows the provider tree and remote module payloads when set.
	Share *Share
	// Hardlink shares unmutated files by link rather than by copy.
	Hardlink bool
}

// Sandbox is a materialised working directory.
type Sandbox struct {
	// Root is the sandbox copy of the closure root.
	Root string
	// ModuleDir is the module directory inside the sandbox.
	ModuleDir string
}

// Materialise builds the sandbox described by the specification.
func Materialise(spec Spec) (Sandbox, error) {
	if err := os.MkdirAll(spec.Target, directoryMode); err != nil {
		return Sandbox{}, fmt.Errorf("creating sandbox: %w", err)
	}

	if err := copyTree(spec); err != nil {
		return Sandbox{}, err
	}

	if err := stage(spec); err != nil {
		return Sandbox{}, err
	}

	built := Sandbox{
		Root:      spec.Target,
		ModuleDir: filepath.Join(spec.Target, filepath.FromSlash(spec.ModuleRel)),
	}

	if spec.Share == nil {
		return built, nil
	}

	if err := shareDataDir(built.ModuleDir, *spec.Share); err != nil {
		return Sandbox{}, err
	}

	return built, nil
}

func copyTree(spec Spec) error {
	return filepath.WalkDir(spec.SourceRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relative, relErr := filepath.Rel(spec.SourceRoot, path)
		if relErr != nil {
			return fmt.Errorf("resolving %s: %w", path, relErr)
		}

		if relative == "." {
			return nil
		}

		slashed := filepath.ToSlash(relative)
		target := filepath.Join(spec.Target, relative)

		if skippedDirs[entry.Name()] {
			// Skipped whatever its type: a symlink wearing a skipped name must
			// not be followed or copied either.
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.IsDir() {
			return os.MkdirAll(target, directoryMode)
		}

		if content, mutated := spec.Mutations[slashed]; mutated {
			return WriteFresh(target, path, content)
		}

		// A staged path is written by stage() as a fresh inode. Sharing it here
		// first would leave the overlay writing through a hardlink to the source
		// tree, which is the one thing the fresh-inode guard exists to refuse.
		if _, staged := spec.Staged[slashed]; staged {
			return nil
		}

		return share(path, target, spec.Hardlink)
	})
}

// stage materialises the overlay: every staged path, whether or not the
// source tree has a file there. Staged content is written last so it wins over
// a copied file of the same name.
func stage(spec Spec) error {
	paths := make([]string, 0, len(spec.Staged))
	for relative := range spec.Staged {
		paths = append(paths, relative)
	}

	slices.Sort(paths)

	for _, relative := range paths {
		target := filepath.Join(spec.Target, filepath.FromSlash(relative))
		source := filepath.Join(spec.SourceRoot, filepath.FromSlash(relative))

		if err := WriteFresh(target, source, spec.Staged[relative]); err != nil {
			return err
		}
	}

	return nil
}

// share links or copies a file the sandbox will not mutate.
func share(source, target string, hardlink bool) error {
	if hardlink && linkable(source) {
		if err := os.Link(source, target); err == nil {
			return nil
		}
	}

	return copyFile(source, target)
}

func linkable(path string) bool {
	name := filepath.Base(path)

	for _, suffix := range linkableSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

func copyFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", source, err)
	}

	input, err := os.Open(source) //nolint:gosec // source paths come from the closure walk.
	if err != nil {
		return fmt.Errorf("reading %s: %w", source, err)
	}

	defer func() { _ = input.Close() }()

	//nolint:gosec // target paths are sandbox-internal.
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}

	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()

		return fmt.Errorf("copying %s: %w", source, err)
	}

	if err := output.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", target, err)
	}

	return nil
}

// WriteFresh writes content to target as a new inode.
//
// The write goes to a temporary file in the same directory and is moved into
// place by rename, so an existing hardlink at target is replaced rather than
// written through. Two assertions bracket it: the target must not already be
// the source file, and the installed file must not share the source's inode.
// Together they are the R2-3 guard — a mutated write can never reach the
// source tree, whatever the caller passed.
func WriteFresh(target, source string, content []byte) error {
	if err := assertDistinct(target, source); err != nil {
		return err
	}

	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("creating %s: %w", directory, err)
	}

	temporary, err := os.CreateTemp(directory, ".tf-mut-*")
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", directory, err)
	}

	name := temporary.Name()

	defer func() { _ = os.Remove(name) }()

	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("writing %s: %w", name, err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}

	if err := os.Chmod(name, fileMode); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", name, err)
	}

	if err := assertDistinct(name, source); err != nil {
		return err
	}

	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("installing %s: %w", target, err)
	}

	return assertDistinct(target, source)
}

// assertDistinct fails when two paths resolve to the same inode. A target that
// does not exist yet is trivially distinct.
func assertDistinct(target, source string) error {
	if source == "" {
		return nil
	}

	sourceInfo, err := os.Stat(source)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("inspecting %s: %w", source, err)
	}

	targetInfo, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("inspecting %s: %w", target, err)
	}

	if os.SameFile(sourceInfo, targetInfo) {
		return fmt.Errorf("%w: %s", ErrSharedInode, source)
	}

	return nil
}

// moduleRecord mirrors one entry of Terraform's own module manifest, whose
// keys are PascalCase.
//
//nolint:tagliatelle // the manifest schema is Terraform's, not ours.
type moduleRecord struct {
	Key    string `json:"Key"`
	Source string `json:"Source"`
	Dir    string `json:"Dir"`
}

//nolint:tagliatelle // the manifest schema is Terraform's, not ours.
type moduleManifest struct {
	Modules []moduleRecord `json:"Modules"`
}

// shareDataDir gives the sandbox its own .terraform directory: a synthesised
// module manifest, a symlink to the shared provider tree, symlinks to the
// immutable remote module payloads, and the dependency lock file.
func shareDataDir(moduleDir string, share Share) error {
	dataDir := filepath.Join(moduleDir, DataDirName)
	if err := os.MkdirAll(dataDir, directoryMode); err != nil {
		return fmt.Errorf("creating %s: %w", dataDir, err)
	}

	if err := linkProviders(dataDir, share.DataDir); err != nil {
		return err
	}

	if err := synthesiseManifest(dataDir, share.DataDir); err != nil {
		return err
	}

	return installLockFile(moduleDir, share.LockFile)
}

func linkProviders(dataDir, warmDataDir string) error {
	source := filepath.Join(warmDataDir, providersDir)
	if _, err := os.Stat(source); err != nil {
		return nil //nolint:nilerr // a module without providers has no tree to share.
	}

	target := filepath.Join(dataDir, providersDir)
	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("sharing provider tree: %w", err)
	}

	return nil
}

func synthesiseManifest(dataDir, warmDataDir string) error {
	warmManifest := filepath.Join(warmDataDir, modulesDirName, modulesFileName)

	content, err := os.ReadFile(warmManifest) //nolint:gosec // warm workspace path.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("reading module manifest: %w", err)
	}

	manifest := moduleManifest{}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return fmt.Errorf("decoding module manifest: %w", err)
	}

	modulesDir := filepath.Join(dataDir, modulesDirName)
	if err := os.MkdirAll(modulesDir, directoryMode); err != nil {
		return fmt.Errorf("creating %s: %w", modulesDir, err)
	}

	vendored := filepath.ToSlash(filepath.Join(DataDirName, modulesDirName)) + "/"

	for _, record := range manifest.Modules {
		if !strings.HasPrefix(filepath.ToSlash(record.Dir), vendored) {
			continue
		}

		if linkErr := linkVendored(record, warmDataDir, dataDir); linkErr != nil {
			return linkErr
		}
	}

	rewritten, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		return fmt.Errorf("encoding module manifest: %w", marshalErr)
	}

	target := filepath.Join(modulesDir, modulesFileName)
	if writeErr := os.WriteFile(target, rewritten, fileMode); writeErr != nil {
		return fmt.Errorf("writing module manifest: %w", writeErr)
	}

	return nil
}

// linkVendored shares one remote module payload, which is immutable and is
// never mutated, into the sandbox at the path its manifest entry names.
func linkVendored(record moduleRecord, warmDataDir, dataDir string) error {
	relative := strings.TrimPrefix(
		filepath.ToSlash(record.Dir),
		filepath.ToSlash(filepath.Join(DataDirName, modulesDirName))+"/",
	)

	name, _, _ := strings.Cut(relative, "/")
	if name == "" {
		return nil
	}

	source := filepath.Join(warmDataDir, modulesDirName, name)
	if _, err := os.Stat(source); err != nil {
		return nil //nolint:nilerr // nothing vendored under that key.
	}

	target := filepath.Join(dataDir, modulesDirName, name)
	if _, err := os.Lstat(target); err == nil {
		return nil
	}

	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("sharing remote module %s: %w", name, err)
	}

	return nil
}

func installLockFile(moduleDir, warmLockFile string) error {
	if warmLockFile == "" {
		return nil
	}

	target := filepath.Join(moduleDir, LockFileName)
	if _, err := os.Stat(target); err == nil {
		return nil
	}

	return copyFile(warmLockFile, target)
}
