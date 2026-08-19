package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/andrewesweet/tf-mut/internal/characterise"
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/sandbox"
)

// The characterisation write is the never-write contract's fifth recorded
// exception, and like the other four it is a tool-owned write the user asks
// for by name.
//
// Two things bind it. The target path set has to be free — a collision is
// refused rather than resolved, and `--force` replaces only files the
// provenance registry marks generated-and-unmodified. And each file carries
// the digest of the input closure that made it green: sources, tests, JSON,
// the lock, the automatically loaded variable files, the mock data, the
// module inventory and the relevant environment. The commit step re-checks
// that digest and the target's absence immediately before each rename, so a
// closure that changed while the scaffold was being verified yields zero
// writes rather than a file that was green for a module that no longer exists.

// RegistryName is the provenance registry's file name, written beside the
// generated suite.
const RegistryName = ".tf-mut-generated.json"

// registryVersion is the registry's own format version.
const registryVersion = "1.0.0"

// insideTheWindow is the commit callback's second invocation: the one
// WriteFreshChecked makes immediately before the rename.
const insideTheWindow = 2

// seedFileMode is the permission the closure-race seam's own writes carry.
// Generated files are written by sandbox.WriteFresh, which sets its own mode;
// this constant belongs to the seam and to nothing else.
const seedFileMode = 0o600

// registry records what this tool generated, so that "generated-unmodified",
// "generated-edited" and "pre-existing" are decided mechanically rather than
// guessed at.
type registry struct {
	Version string `json:"version"`
	// Files maps a module-relative path to what was written there.
	Files map[string]registryFile `json:"files"`
}

type registryFile struct {
	// Digest is the content digest as written.
	Digest string `json:"digest"`
	// InputDigest is the input closure that made the content green.
	InputDigest string `json:"input_digest"`
	// Pins lists the identifiers of the pins the file carries.
	Pins []string `json:"pins"`
}

// commitScaffold performs the write, or refuses it and says why.
//
// A refusal that happens before any rename leaves nothing behind and is an
// error. A failure after the first rename has left the caller a partial state,
// and an error alone would not say which files moved — so the write record
// carries the paths and the caller keeps the report.
func commitScaffold(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	block *report.Characterisation,
	files []generated,
) error {
	// The commit's own target set is excluded from the digest: the probe is
	// there to catch a change somebody else made, and a commit that tripped
	// over the file it just placed would refuse every write after the first.
	targets := targetPaths(configuration, files)

	inputDigest, err := InputClosureDigest(configuration, settings, prepared, targets)
	if err != nil {
		return err
	}

	record := &report.CharacteriseWrite{
		Requested: true, InputDigest: inputDigest,
		Written: []string{}, Refused: "", Partial: nil,
	}
	block.Write = record

	existing := loadRegistry(configuration.ModuleDir)

	// The registry is part of the target path set and is checked with
	// everything else in it. It was excluded once, and an ordinary `--write`
	// then replaced a registry a user owned — a tool-owned write over a file
	// the tool had never written.
	if refusal := checkRegistry(configuration); refusal != "" {
		record.Refused = refusal

		return fmt.Errorf("%w: %s", ErrWriteRefused, refusal)
	}

	if refusal := checkTargets(configuration, settings, entriesOf(files), existing); refusal != "" {
		record.Refused = refusal

		return fmt.Errorf("%w: %s", ErrWriteRefused, refusal)
	}

	written, err := writeFiles(configuration, settings, prepared, inputDigest, block, files, targets)
	record.Written = written

	if err != nil {
		record.Partial = written
		record.Refused = err.Error()

		// The ledger has to describe the tree the tool actually left behind.
		// Without this the files that did land are absent from the registry,
		// and `checkTargets` then refuses to let `--force` replace them —
		// "not in the provenance registry, so --force will not replace it" —
		// so the tool's own output becomes indistinguishable from a user's and
		// the only recovery is manual deletion. A registry write that fails on
		// top of a failed commit changes nothing about the first failure, so
		// its error is deliberately not allowed to displace it.
		if len(written) > 0 {
			_ = storeRegistry(configuration.ModuleDir, settings, block, files, inputDigest, existing)
		}

		return err
	}

	block.Staged = false

	if err := storeRegistry(configuration.ModuleDir, settings, block, files, inputDigest, existing); err != nil {
		// Every generated file has already been renamed by this point, so a
		// registry that will not store is a partial state and not a refusal:
		// the caller's tree has changed and the record of what changed it has
		// not. Without this the report is discarded as a pre-write error and
		// the written files are invisible.
		record.Partial = written
		record.Refused = err.Error()

		return err
	}

	return nil
}

// checkRegistry applies the collision protocol to the provenance registry.
//
// The registry cannot be proven by digest the way a generated test file is —
// a file cannot record its own content hash — so it is proven by *shape*: a
// file at this path that parses as a registry of the current format is one
// this tool wrote, and anything else is somebody's, whatever `--force` says.
// A tool-owned write over a file the tool never wrote is the thing the
// never-write contract exists to prevent, and `--force` is permission to
// replace this tool's own output, not permission to replace a file that
// happens to share its name.
func checkRegistry(configuration discovery.Configuration) string {
	path := filepath.Join(configuration.ModuleDir, RegistryName)

	content, err := os.ReadFile(path) //nolint:gosec // a module-relative tool file.
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}

	if err != nil {
		return fmt.Sprintf("%s could not be inspected: %v", RegistryName, err)
	}

	loaded := registry{Version: "", Files: nil}
	if json.Unmarshal(content, &loaded) != nil || loaded.Version != registryVersion {
		return fmt.Sprintf("%s exists and is not a provenance registry this version wrote, "+
			"so it will not be replaced: move it aside if it is not yours", RegistryName)
	}

	// A registry this tool wrote is updated without `--force`. It is the
	// tool's own ledger and every legitimate second write has to extend it —
	// a resume that promoted an answer, a `--force` that replaced a suite.
	// What `--force` governs is the *generated suite*; what this check
	// governs is whether the ledger is ours at all.
	return ""
}

// checkTargets applies the collision rule over the full target path set.
func checkTargets(
	configuration discovery.Configuration,
	settings Config,
	files []report.GeneratedFile,
	existing registry,
) string {
	for _, file := range files {
		target := filepath.Join(configuration.ModuleDir, filepath.FromSlash(file.Path))

		content, err := os.ReadFile(target) //nolint:gosec // a module-relative generated path.
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return fmt.Sprintf("%s could not be inspected: %v", file.Path, err)
		}

		if !settings.CharacteriseForce {
			return fmt.Sprintf("%s already exists; pass --force to replace a file this tool "+
				"generated and nobody has edited", file.Path)
		}

		recorded, generated := existing.Files[file.Path]
		if !generated {
			return fmt.Sprintf("%s is not in the provenance registry, so --force will not "+
				"replace it: it was not generated by this tool", file.Path)
		}

		if recorded.Digest != characterise.Digest(content) {
			return fmt.Sprintf("%s has been edited since it was generated, so --force will "+
				"not replace it", file.Path)
		}
	}

	return ""
}

// writeFiles installs each generated file atomically, re-checking the input
// closure and the target immediately before every rename.
func writeFiles(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	inputDigest string,
	block *report.Characterisation,
	files []generated,
	targets map[string]bool,
) ([]string, error) {
	written := []string{}

	for _, file := range files {
		if len(written) == settings.SeedClosureAfter && !settings.SeedRenameWindowChange {
			if err := seedClosureChange(configuration, settings); err != nil {
				return written, err
			}
		}

		target := filepath.Join(configuration.ModuleDir, filepath.FromSlash(file.entry.Path))

		// The same two questions the loop asked, asked again in the instant
		// before the rename: everything between them and here — creating,
		// writing, closing and chmodding a temporary file — is time in which
		// somebody else could have edited a source or created this target.
		// The seam fires only on the *second* call — the one the rename
		// boundary makes — so that a protocol which checked before the window
		// and not inside it would see nothing and write.
		calls := 0
		commit := func() error {
			calls++

			if settings.SeedRenameWindowChange && calls == insideTheWindow {
				if err := seedClosureChange(configuration, settings); err != nil {
					return err
				}
			}

			return recheckWrite(configuration, settings, prepared, inputDigest, targets, file.entry)
		}

		if err := commit(); err != nil {
			return written, err
		}

		if err := sandbox.WriteFreshChecked(target, "", file.bytes, commit); err != nil {
			markWritten(block, written)

			return written, err
		}

		written = append(written, file.entry.Path)
	}

	markWritten(block, written)

	return written, nil
}

// targetPaths is the absolute path set this commit is placing.
func targetPaths(configuration discovery.Configuration, files []generated) map[string]bool {
	targets := map[string]bool{}

	for _, file := range files {
		targets[filepath.Join(configuration.ModuleDir,
			filepath.FromSlash(file.entry.Path))] = true
	}

	// The registry is written by the commit too, and is no more a change
	// somebody else made than the suite is.
	targets[filepath.Join(configuration.ModuleDir, RegistryName)] = true

	return targets
}

// recheckWrite asks the write protocol's two questions: is the closure the one that
// made this content green, and is the target still free?
func recheckWrite(
	configuration discovery.Configuration,
	settings Config,
	prepared warm,
	inputDigest string,
	targets map[string]bool,
	file report.GeneratedFile,
) error {
	current, err := InputClosureDigest(configuration, settings, prepared, targets)
	if err != nil {
		return err
	}

	if current != inputDigest {
		return fmt.Errorf("%w: the input closure changed while the suite was being "+
			"verified, so the generated content is green for a module that no longer "+
			"exists (%s became %s)", ErrWriteRefused, inputDigest, current)
	}

	if refusal := checkTargets(configuration, settings, []report.GeneratedFile{file},
		loadRegistry(configuration.ModuleDir)); refusal != "" {
		return fmt.Errorf("%w: %s", ErrWriteRefused, refusal)
	}

	return nil
}

// seedClosureChange stages the race the commit step exists to close: a source
// file that moved between the verification that made the scaffold green and
// the rename that would install it. It fires once, before the first rename.
func seedClosureChange(configuration discovery.Configuration, settings Config) error {
	if settings.SeedClosureFile != "" {
		added := filepath.Join(configuration.ModuleDir,
			filepath.FromSlash(settings.SeedClosureFile))
		if err := os.WriteFile(added, []byte("# staged closure addition\n"),
			seedFileMode); err != nil {
			return fmt.Errorf("staging the closure addition: %w", err)
		}
	}

	if settings.SeedClosureChange == "" {
		return nil
	}

	target := filepath.Join(configuration.ModuleDir, filepath.FromSlash(settings.SeedClosureChange))

	//nolint:gosec // a seam control's own path, and a test-owned tree.
	file, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, seedFileMode)
	if err != nil {
		return fmt.Errorf("staging the closure change: %w", err)
	}

	if _, err := file.WriteString("\n# staged closure change\n"); err != nil {
		_ = file.Close()

		return fmt.Errorf("staging the closure change: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("staging the closure change: %w", err)
	}

	return nil
}

func markWritten(block *report.Characterisation, written []string) {
	for index, file := range block.Files {
		if slices.Contains(written, file.Path) {
			block.Files[index].Written = true
		}
	}
}

// loadRegistry reads the provenance registry, treating anything unreadable as
// an empty one: a registry that cannot be read claims nothing, and claiming
// nothing is the safe direction — it makes --force refuse rather than replace.
func loadRegistry(moduleDir string) registry {
	empty := registry{Version: registryVersion, Files: map[string]registryFile{}}

	content, err := os.ReadFile(filepath.Join(moduleDir, RegistryName)) //nolint:gosec // a module-relative tool file.
	if err != nil {
		return empty
	}

	loaded := registry{Version: "", Files: nil}
	if err := json.Unmarshal(content, &loaded); err != nil || loaded.Version != registryVersion {
		return empty
	}

	if loaded.Files == nil {
		loaded.Files = map[string]registryFile{}
	}

	return loaded
}

// storeRegistry records what this write generated, merged over what earlier
// writes recorded.
func storeRegistry(
	moduleDir string,
	settings Config,
	block *report.Characterisation,
	files []generated,
	inputDigest string,
	existing registry,
) error {
	if settings.SeedRegistryFailure {
		return fmt.Errorf("%w: the provenance registry could not be stored", ErrWriteRefused)
	}

	// The digest the registry records is the *written* bytes', which is what
	// `checkTargets` compares against the file on disk. The report's published
	// digest covers the redacted view and would never match one.
	written := map[string]string{}
	for _, file := range files {
		written[file.entry.Path] = file.digest
	}

	for _, file := range block.Files {
		if !file.Written {
			continue
		}

		existing.Files[file.Path] = registryFile{
			Digest: written[file.Path], InputDigest: inputDigest, Pins: pinsIn(block, file.Path),
		}
	}

	existing.Version = registryVersion

	content, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the provenance registry: %w", err)
	}

	// The registry goes through the same rename-window protocol as every other
	// file in the target set. `checkRegistry` runs at the top of
	// `commitScaffold`, so without this callback the window between "there is
	// no registry here" and this write spans every generated file's write, and
	// a registry created inside it by a concurrent invocation — or by a user —
	// would be overwritten with no shape check at all.
	//nolint:exhaustruct // the registry check reads the module directory and nothing else.
	recheck := func() error {
		if refusal := checkRegistry(discovery.Configuration{ModuleDir: moduleDir}); refusal != "" {
			return fmt.Errorf("%w: %s", ErrWriteRefused, refusal)
		}

		return nil
	}

	return sandbox.WriteFreshChecked(filepath.Join(moduleDir, RegistryName), "",
		append(content, '\n'), recheck)
}

// pinsIn lists the pins a generated file carries.
func pinsIn(block *report.Characterisation, path string) []string {
	scenarios := map[string]bool{}

	for _, scenario := range block.Scenarios {
		if scenario.File == path {
			scenarios[scenario.ID] = true
		}
	}

	pins := []string{}

	for _, pin := range block.Pins {
		if pin.Status == report.Pinned && scenarios[pin.Scenario] {
			pins = append(pins, pin.ID)
		}
	}

	return pins
}
